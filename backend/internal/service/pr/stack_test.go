package pr

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
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
	// onMerge runs once the provider has taken the merge.
	onMerge func()
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
	if f.onMerge != nil {
		f.onMerge()
	}
	return f.status, f.mergeErr
}

// A reader answering per PR, unlike the single-observation fake.
type stackReader struct {
	byNumber map[int]ports.SCMObservation
	review   ports.SCMReviewObservation
	err      error
}

func (r *stackReader) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	if r.err != nil {
		return nil, r.err
	}
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
		// Settled: queued app suites only, since well before noChecksSettle.
		HeadChecks: &fakeHeadChecks{facts: ports.SCMHeadChecks{Suites: 3, Since: time.Unix(100, 0).Add(-3 * time.Minute)}},
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

// GitHub reports no status rollup for a head nothing checks, so its CI reads
// as unknown. Only a layer read with no checks at all, settled as the provider
// confirms, says so; one whose checks weren't read, or whose run was
// cancelled, is still unknown.
func TestStack_SaysWhichLayersHaveNoChecks(t *testing.T) {
	f := newStackFixture(2)
	unchecked := readyLayer(1, headA)
	unchecked.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: headA}
	cancelled := readyLayer(2, headB)
	cancelled.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: headB, FailedChecks: []ports.SCMCheckObservation{{Name: "test", Status: "cancelled"}}}
	f.reader.byNumber = map[int]ports.SCMObservation{1: unchecked, 2: cancelled, 3: {}}
	got, err := f.svc.Stack(context.Background(), "2", "https://github.com/acme/widgets/pull/2")
	if err != nil || got.Stack == nil {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	noChecks := []bool{got.Stack.Layers[0].NoChecks, got.Stack.Layers[1].NoChecks, got.Stack.Layers[2].NoChecks}
	if !noChecks[0] || noChecks[1] || noChecks[2] {
		t.Fatalf("noChecks = %v, want only the layer read with no checks", noChecks)
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

func TestMergeStack_LandsLayersNothingChecks(t *testing.T) {
	f := newStackFixture(2)
	for number, head := range map[int]string{1: headA, 2: headB} {
		layer := readyLayer(number, head)
		layer.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: head}
		f.reader.byNumber[number] = layer
	}
	// Not asked, as the CLI: the layers wait for checks.
	if _, err := f.svc.MergeStack(context.Background(), StackMergeRequest{PRID: "2", PRURL: "https://github.com/acme/widgets/pull/2", ExpectedHeads: headsThrough(1, 2)}); !errors.Is(err, ErrPRPreconditions) || len(f.stacker.merge) != 0 {
		t.Fatalf("unasked err = %v, merge = %#v", err, f.stacker.merge)
	}
	got, err := f.svc.MergeStack(context.Background(), StackMergeRequest{PRID: "2", PRURL: "https://github.com/acme/widgets/pull/2", ExpectedHeads: headsThrough(1, 2), AllowNoChecks: true})
	if err != nil || len(got.Numbers) != 2 || len(f.stacker.merge) != 1 {
		t.Fatalf("got = %#v, err = %v, merge = %#v", got, err, f.stacker.merge)
	}
}

// A layer pushed seconds ago reads the same as one nothing checks, so it waits.
func TestStack_ALayerJustPushedIsNotNoChecks(t *testing.T) {
	f := newStackFixture(2)
	f.svc.headChecks = &fakeHeadChecks{facts: ports.SCMHeadChecks{Suites: 3, Since: time.Unix(100, 0).Add(-10 * time.Second)}}
	for number, head := range map[int]string{1: headA, 2: headB} {
		layer := readyLayer(number, head)
		layer.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: head}
		f.reader.byNumber[number] = layer
	}
	got, err := f.svc.Stack(context.Background(), "2", "https://github.com/acme/widgets/pull/2")
	if err != nil || got.Stack == nil || got.Stack.Layers[0].NoChecks || got.Stack.Layers[1].NoChecks {
		t.Fatalf("got = %#v, err = %v", got, err)
	}
	if _, err := f.svc.MergeStack(context.Background(), StackMergeRequest{PRID: "2", PRURL: "https://github.com/acme/widgets/pull/2", ExpectedHeads: headsThrough(1, 2), AllowNoChecks: true}); !errors.Is(err, ErrPRPreconditions) || len(f.stacker.merge) != 0 {
		t.Fatalf("err = %v, merge = %#v", err, f.stacker.merge)
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

// layerBook is the store, merge writer and lifecycle behind a stack merge. It
// keeps one row per layer and logs each bookkeeping step in order. Like the
// real ones, every step fails on a cancelled context.
type layerBook struct {
	rows     map[string]domain.PullRequest
	steps    []string
	observed []ports.SCMObservation
}

func layerURL(number int) string {
	return "https://github.com/acme/widgets/pull/" + strconv.Itoa(number)
}

func (b *layerBook) GetPR(ctx context.Context, url string) (domain.PullRequest, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.PullRequest{}, false, err
	}
	pr, ok := b.rows[url]
	return pr, ok, nil
}

func (b *layerBook) RecordWorkerPRMergedVia(ctx context.Context, prURL, mergedVia string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.steps = append(b.steps, "via "+mergedVia+" "+prURL)
	return nil
}

func (b *layerBook) MarkPRMerged(ctx context.Context, prURL, _ string, _ time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.steps = append(b.steps, "merged "+prURL)
	return nil
}

func (b *layerBook) ApplySCMObservation(ctx context.Context, sessionID domain.SessionID, obs ports.SCMObservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.steps = append(b.steps, "lifecycle "+string(sessionID)+" "+obs.PR.URL)
	b.observed = append(b.observed, obs)
	return nil
}

// newLandingFixture tracks every layer of the stack, each in its own session.
func newLandingFixture() (stackFixture, *layerBook, *fakeRefresher) {
	f := newStackFixture(2)
	book := &layerBook{rows: map[string]domain.PullRequest{}}
	for n := 1; n <= 3; n++ {
		book.rows[layerURL(n)] = domain.PullRequest{URL: layerURL(n), Number: n, Provider: "github", Host: "github.com", Repo: "acme/widgets", SessionID: domain.SessionID("s-" + strconv.Itoa(n))}
	}
	refresher := &fakeRefresher{}
	f.svc = NewActionService(ActionDeps{
		Store:       book,
		Reader:      f.reader,
		Stacker:     f.stacker,
		MergeWriter: book,
		Lifecycle:   book,
		Refresher:   refresher,
		Clock:       func() time.Time { return time.Unix(100, 0) },
	})
	return f, book, refresher
}

func mergeStackThrough2(ctx context.Context, f stackFixture) (StackMergeResult, error) {
	return f.svc.MergeStack(ctx, StackMergeRequest{PRID: "2", PRURL: layerURL(2), ExpectedHeads: headsThrough(1, 2)})
}

// A merged row is terminal, so the observer's one forced refresh was lifecycle's
// only chance to see the merge: a failed read meant no teardown and no merged
// notification. Lifecycle hears of every landed layer however reads go after.
func TestMergeStack_TellsLifecycleAboutEveryLandedLayer(t *testing.T) {
	f, book, refresher := newLandingFixture()
	f.stacker.onMerge = func() { f.reader.err = errors.New("rate limited") }
	if _, err := mergeStackThrough2(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if len(book.observed) != 2 {
		t.Fatalf("lifecycle saw %d layers, want 2: %v", len(book.observed), book.steps)
	}
	for i, obs := range book.observed {
		number := i + 1
		if obs.PR.URL != layerURL(number) || !obs.Fetched || !obs.PR.Merged || obs.PR.Closed || obs.PR.State != string(domain.PRStateMerged) || !obs.ObservedAt.Equal(time.Unix(100, 0).UTC()) {
			t.Fatalf("layer %d observation = %+v", number, obs)
		}
	}
	want := []string{
		"merged " + layerURL(1), "lifecycle s-1 " + layerURL(1),
		"merged " + layerURL(2), "lifecycle s-2 " + layerURL(2),
	}
	if !slices.Equal(withoutVia(book.steps), want) {
		t.Fatalf("steps = %v, want %v", book.steps, want)
	}
	if !slices.Equal(refresher.urls, []string{layerURL(1), layerURL(2)}) {
		t.Fatalf("refreshed = %v", refresher.urls)
	}
}

func withoutVia(steps []string) []string {
	return slices.DeleteFunc(slices.Clone(steps), func(step string) bool { return strings.HasPrefix(step, "via ") })
}

// ao.usage.worker_pr_merged attributes a stacked merge to the client that asked
// for it, as it does a plain merge, and each layer is attributed before its row
// turns merged so the usage flush never reports it unattributed.
func TestMergeStack_RecordsMergedViaForEveryLandedLayer(t *testing.T) {
	for name, tc := range map[string]struct {
		ctx  context.Context
		want string
	}{
		"from the desktop": {ctx: telemetrymeta.WithSurface(context.Background(), domain.RequestSurfaceDesktop), want: deliverytelemetry.MergedViaDesktop},
		"from the CLI":     {ctx: context.Background(), want: deliverytelemetry.MergedViaCLI},
	} {
		t.Run(name, func(t *testing.T) {
			f, book, _ := newLandingFixture()
			if _, err := mergeStackThrough2(tc.ctx, f); err != nil {
				t.Fatal(err)
			}
			want := []string{
				"via " + tc.want + " " + layerURL(1), "merged " + layerURL(1), "lifecycle s-1 " + layerURL(1),
				"via " + tc.want + " " + layerURL(2), "merged " + layerURL(2), "lifecycle s-2 " + layerURL(2),
			}
			if !slices.Equal(book.steps, want) {
				t.Fatalf("steps = %v, want %v", book.steps, want)
			}
		})
	}
}

// A slow merge outlives its request: the context is gone by the time GitHub
// answers. The layers it took are still recorded, and still attributed.
func TestMergeStack_RecordsLayersAfterTheRequestIsCancelled(t *testing.T) {
	for name, tc := range map[ports.SCMStackMergeStatus][]string{
		ports.SCMStackMerged: {
			"via desktop " + layerURL(1), "merged " + layerURL(1), "lifecycle s-1 " + layerURL(1),
			"via desktop " + layerURL(2), "merged " + layerURL(2), "lifecycle s-2 " + layerURL(2),
		},
		// Still landing: the rows stay open for the observer to see them merge.
		ports.SCMStackPending:  nil,
		ports.SCMStackEnqueued: nil,
	} {
		t.Run(string(name), func(t *testing.T) {
			f, book, refresher := newLandingFixture()
			ctx, cancel := context.WithCancel(telemetrymeta.WithSurface(context.Background(), domain.RequestSurfaceDesktop))
			defer cancel()
			f.stacker.status = name
			f.stacker.onMerge = cancel
			got, err := mergeStackThrough2(ctx, f)
			if err != nil || got.Status != string(name) {
				t.Fatalf("got=%#v err=%v", got, err)
			}
			if !slices.Equal(book.steps, tc) {
				t.Fatalf("steps = %v, want %v", book.steps, tc)
			}
			if !slices.Equal(refresher.urls, []string{layerURL(1), layerURL(2)}) {
				t.Fatalf("refreshed = %v", refresher.urls)
			}
		})
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
