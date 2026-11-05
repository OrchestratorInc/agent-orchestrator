package pr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	headC = "cccccccccccccccccccccccccccccccccccccccc"
)

type fakeStacker struct {
	stacks      map[int]ports.SCMStack
	stackErr    error
	parent      ports.SCMPRRef
	hasParent   bool
	registered  []ports.SCMStackRegisterRequest
	registerErr error
	merge       []ports.SCMStackMergeRequest
	status      ports.SCMStackMergeStatus
	mergeErr    error
}

func (f *fakeStacker) PullRequestStack(_ context.Context, ref ports.SCMPRRef) (ports.SCMStack, bool, error) {
	stack, ok := f.stacks[ref.Number]
	return stack, ok, f.stackErr
}

func (f *fakeStacker) FindOpenPullRequest(context.Context, ports.SCMRepo, string) (ports.SCMPRRef, bool, error) {
	return f.parent, f.hasParent, nil
}

func (f *fakeStacker) RegisterStacked(_ context.Context, request ports.SCMStackRegisterRequest) (ports.SCMStack, error) {
	f.registered = append(f.registered, request)
	return threeLayers(), f.registerErr
}

func (f *fakeStacker) MergeStack(_ context.Context, request ports.SCMStackMergeRequest) (ports.SCMStackMergeStatus, error) {
	f.merge = append(f.merge, request)
	return f.status, f.mergeErr
}

// A reader answering per PR, unlike the single-observation fake.
type stackReader struct {
	byNumber map[int]ports.SCMObservation
	review   ports.SCMReviewObservation
}

func (r *stackReader) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	out := make([]ports.SCMObservation, 0, len(refs))
	for _, ref := range refs {
		out = append(out, r.byNumber[ref.Number])
	}
	return out, nil
}

func (r *stackReader) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	return r.review, nil
}

func threeLayers() ports.SCMStack {
	return ports.SCMStack{Number: 9, BaseBranch: "main", Open: true, Layers: []ports.SCMStackLayer{
		{Number: 1, Open: true, HeadBranch: "one", HeadSHA: headA},
		{Number: 2, Open: true, HeadBranch: "two", HeadSHA: headB},
		{Number: 3, Open: true, HeadBranch: "three", HeadSHA: headC},
	}}
}

func readyLayer(number int, head string) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched:      true,
		PR:           ports.SCMPRObservation{URL: "https://github.com/acme/widgets/pull/" + string(rune('0'+number)), Number: number, HeadSHA: head, Title: "Layer"},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: head},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
	}
}

type stackFixture struct {
	svc     *ActionService
	stacker *fakeStacker
	reader  *stackReader
	writer  *fakeActionWriter
}

func newStackFixture(number int) stackFixture {
	stack := threeLayers()
	tracked := domain.PullRequest{URL: "https://github.com/acme/widgets/pull/2", Number: number, Provider: "github", Host: "github.com", Repo: "acme/widgets", SessionID: "s-1", TargetBranch: "one"}
	stacker := &fakeStacker{stacks: map[int]ports.SCMStack{1: stack, 2: stack, 3: stack}, status: ports.SCMStackMerged}
	reader := &stackReader{byNumber: map[int]ports.SCMObservation{1: readyLayer(1, headA), 2: readyLayer(2, headB), 3: readyLayer(3, headC)}}
	writer := &fakeActionWriter{}
	svc := NewActionService(ActionDeps{
		Store:       &fakeActionStore{pr: tracked, ok: true},
		Reader:      reader,
		Stacker:     stacker,
		MergeWriter: writer,
		Clock:       func() time.Time { return time.Unix(100, 0) },
	})
	return stackFixture{svc: svc, stacker: stacker, reader: reader, writer: writer}
}

func headsThrough(numbers ...int) []StackHead {
	all := map[int]string{1: headA, 2: headB, 3: headC}
	out := make([]StackHead, 0, len(numbers))
	for _, n := range numbers {
		out = append(out, StackHead{Number: n, HeadSHA: all[n]})
	}
	return out
}

func TestStack_DescribesEveryLayer(t *testing.T) {
	f := newStackFixture(2)
	f.stacker.stacks[2] = func() ports.SCMStack {
		stack := threeLayers()
		stack.Layers[0].Open, stack.Layers[0].Merged = false, true
		stack.Layers[2].Draft = true
		return stack
	}()
	got, err := f.svc.Stack(context.Background(), "2", "https://github.com/acme/widgets/pull/2")
	if err != nil || got.Stack == nil {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	states := []domain.PRState{got.Stack.Layers[0].State, got.Stack.Layers[1].State, got.Stack.Layers[2].State}
	if states[0] != domain.PRStateMerged || states[1] != domain.PRStateOpen || states[2] != domain.PRStateDraft {
		t.Fatalf("states = %v", states)
	}
	if got.Stack.Layers[1].Title != "Layer" || got.Stack.Layers[1].CI != string(domain.CIPassing) {
		t.Fatalf("layer = %#v", got.Stack.Layers[1])
	}
}

func TestStack_OffersTheParentOnlyWhenItCanTakeALayer(t *testing.T) {
	ref := ports.SCMPRRef{Number: 7, URL: "https://github.com/acme/widgets/pull/7"}
	for name, tc := range map[string]struct {
		parentStack *ports.SCMStack
		want        bool
	}{
		"parent is alone":        {want: true},
		"parent tops a stack":    {parentStack: &ports.SCMStack{Number: 4, Open: true, Layers: []ports.SCMStackLayer{{Number: 6}, {Number: 7}}}, want: true},
		"parent is mid-stack":    {parentStack: &ports.SCMStack{Number: 4, Open: true, Layers: []ports.SCMStackLayer{{Number: 7}, {Number: 8}}}},
		"parent's stack is done": {parentStack: &ports.SCMStack{Number: 4, Layers: []ports.SCMStackLayer{{Number: 7}}}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newStackFixture(2)
			f.stacker.stacks = map[int]ports.SCMStack{}
			if tc.parentStack != nil {
				f.stacker.stacks[7] = *tc.parentStack
			}
			f.stacker.parent, f.stacker.hasParent = ref, true
			got, err := f.svc.Stack(context.Background(), "2", "https://github.com/acme/widgets/pull/2")
			if err != nil {
				t.Fatal(err)
			}
			if (got.Parent != nil) != tc.want {
				t.Fatalf("parent = %#v", got.Parent)
			}
		})
	}
}

func TestStack_NoStackerMeansNothingToShow(t *testing.T) {
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: domain.PullRequest{URL: "u", Number: 2, Repo: "a/b"}, ok: true}})
	got, err := svc.Stack(context.Background(), "2", "u")
	if err != nil || got.Stack != nil || got.Parent != nil {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestAddToStack_RegistersOnTheParent(t *testing.T) {
	f := newStackFixture(2)
	f.stacker.stacks = map[int]ports.SCMStack{}
	f.stacker.parent, f.stacker.hasParent = ports.SCMPRRef{Number: 1}, true
	got, err := f.svc.AddToStack(context.Background(), "2", "https://github.com/acme/widgets/pull/2")
	if err != nil || got.Stack == nil {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	if len(f.stacker.registered) != 1 || f.stacker.registered[0].Parent != 1 || f.stacker.registered[0].Child != 2 {
		t.Fatalf("registered = %#v", f.stacker.registered)
	}
}

func TestAddToStack_MapsRefusal(t *testing.T) {
	f := newStackFixture(2)
	f.stacker.stacks = map[int]ports.SCMStack{}
	f.stacker.parent, f.stacker.hasParent = ports.SCMPRRef{Number: 1}, true
	f.stacker.registerErr = ports.ErrSCMRejected
	if _, err := f.svc.AddToStack(context.Background(), "2", "https://github.com/acme/widgets/pull/2"); !errors.Is(err, ErrPRRejected) {
		t.Fatalf("err = %v", err)
	}
}

func TestMergeStack_LandsTheLayersUpToThisPR(t *testing.T) {
	f := newStackFixture(2)
	got, err := f.svc.MergeStack(context.Background(), StackMergeRequest{PRID: "2", PRURL: "https://github.com/acme/widgets/pull/2", ExpectedHeads: headsThrough(1, 2)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Numbers) != 2 || got.Numbers[0] != 1 || got.Numbers[1] != 2 || got.Status != "merged" || got.Method != "squash" {
		t.Fatalf("got = %#v", got)
	}
	if len(f.stacker.merge) != 1 || f.stacker.merge[0].ExpectedHeadSHA != headB || f.stacker.merge[0].PR.Number != 2 {
		t.Fatalf("merge = %#v", f.stacker.merge)
	}
}

func TestMergeStack_SkipsLayersAlreadyMerged(t *testing.T) {
	f := newStackFixture(2)
	stack := threeLayers()
	stack.Layers[0].Merged, stack.Layers[0].Open = true, false
	f.stacker.stacks[2] = stack
	got, err := f.svc.MergeStack(context.Background(), StackMergeRequest{PRID: "2", PRURL: "https://github.com/acme/widgets/pull/2", ExpectedHeads: headsThrough(2)})
	if err != nil || len(got.Numbers) != 1 {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestMergeStack_FailsClosed(t *testing.T) {
	for name, tc := range map[string]struct {
		edit  func(*stackFixture)
		heads []StackHead
		want  error
	}{
		"a layer was pushed to": {
			heads: []StackHead{{Number: 1, HeadSHA: headC}, {Number: 2, HeadSHA: headB}},
			want:  ErrPRHeadChanged,
		},
		"a layer appeared below": {
			heads: headsThrough(2),
			want:  ErrPRHeadChanged,
		},
		"the fresh head moved": {
			edit:  func(f *stackFixture) { f.reader.byNumber[1] = readyLayer(1, headC) },
			heads: headsThrough(1, 2),
			want:  ErrPRHeadChanged,
		},
		"a lower layer is a draft": {
			edit: func(f *stackFixture) {
				stack := threeLayers()
				stack.Layers[0].Draft = true
				f.stacker.stacks[2] = stack
			},
			heads: headsThrough(1, 2),
			want:  ErrPRNotMergeable,
		},
		"a lower layer's checks fail": {
			edit: func(f *stackFixture) {
				layer := readyLayer(1, headA)
				layer.CI.Summary = string(domain.CIFailing)
				f.reader.byNumber[1] = layer
			},
			heads: headsThrough(1, 2),
			want:  ErrPRPreconditions,
		},
		"the stack closed": {
			edit: func(f *stackFixture) {
				stack := threeLayers()
				stack.Open = false
				f.stacker.stacks[2] = stack
			},
			heads: headsThrough(1, 2),
			want:  ErrPRHeadChanged,
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newStackFixture(2)
			if tc.edit != nil {
				tc.edit(&f)
			}
			_, err := f.svc.MergeStack(context.Background(), StackMergeRequest{PRID: "2", PRURL: "https://github.com/acme/widgets/pull/2", ExpectedHeads: tc.heads})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(f.stacker.merge) != 0 {
				t.Fatalf("merged anyway: %#v", f.stacker.merge)
			}
		})
	}
}

func TestGuardStackedMerge_RefusesALayerAboveUnmergedOnes(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*ports.SCMStack)
		pr   int
		want bool
	}{
		"bottom layer":       {pr: 1},
		"middle layer":       {pr: 2, want: true},
		"lower layer landed": {pr: 2, edit: func(s *ports.SCMStack) { s.Layers[0].Merged = true }},
		"closed stack":       {pr: 2, edit: func(s *ports.SCMStack) { s.Open = false }},
	} {
		t.Run(name, func(t *testing.T) {
			f := newStackFixture(tc.pr)
			stack := threeLayers()
			if tc.edit != nil {
				tc.edit(&stack)
			}
			f.stacker.stacks[tc.pr] = stack
			err := f.svc.guardStackedMerge(context.Background(), ports.SCMPRRef{Number: tc.pr})
			if got := errors.Is(err, ErrPRStacked); got != tc.want {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestGuardStackedMerge_NotKnowingDoesNotBlock(t *testing.T) {
	f := newStackFixture(2)
	f.stacker.stackErr = errors.New("rate limited")
	if err := f.svc.guardStackedMerge(context.Background(), ports.SCMPRRef{Number: 2}); err != nil {
		t.Fatal(err)
	}
}
