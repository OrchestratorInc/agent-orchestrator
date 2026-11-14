package pr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// StackLayer is one pull request of a stack as the PR tab lists it.
type StackLayer struct {
	Number         int
	Title          string
	URL            string
	State          domain.PRState
	HeadBranch     string
	HeadSHA        string
	CI             string
	ReviewDecision string
	Mergeability   string
	// NoChecks says the layer's CI is unknown because nothing checks its
	// head, as the provider confirms (see noChecksSettled), not because its
	// checks weren't read or haven't started yet.
	NoChecks bool
	// SessionID is the AO session tracking this layer, when one does.
	SessionID domain.SessionID
}

// Stack is a provider-native stack, bottom layer first.
type Stack struct {
	Number     int
	URL        string
	BaseBranch string
	Open       bool
	Layers     []StackLayer
}

// StackParent is the open pull request a PR is based on, which it could be
// stacked on but isn't yet.
type StackParent struct {
	Number int
	URL    string
}

// StackResult is what's known of a PR's place in a stack. Neither field is set
// for a PR with no stack and no parent, or on a provider without stacks.
type StackResult struct {
	Stack  *Stack
	Parent *StackParent
}

// StackHead pins one layer to the commit the user saw.
type StackHead struct {
	Number  int
	HeadSHA string
}

// StackMergeRequest lands PRID and the unmerged layers below it. ExpectedHeads
// names every one of those layers, so a push or a new layer stops the merge.
type StackMergeRequest struct {
	PRID          string
	PRURL         string
	Method        string
	ExpectedHeads []StackHead
	// AllowNoChecks lets layers that nothing checks land, as MergeRequest's does.
	AllowNoChecks bool
}

// StackMergeResult reports the layers a stack merge covered. Status says
// whether they landed, joined the merge queue, or are still landing.
type StackMergeResult struct {
	PRNumber       int
	Numbers        []int
	Method         string
	Status         string
	StateChangedAt time.Time
}

// Stack reads the stack holding a tracked PR, or the parent it could join.
func (s *ActionService) Stack(ctx context.Context, prID, prURL string) (StackResult, error) {
	tracked, repo, err := s.loadStackPR(ctx, prID, prURL)
	if err != nil {
		return StackResult{}, err
	}
	if s.stacker == nil {
		return StackResult{}, nil
	}
	ref := ports.SCMPRRef{Repo: repo, Number: tracked.Number, URL: tracked.URL}
	stack, inStack, err := s.stacker.PullRequestStack(ctx, ref)
	if errors.Is(err, ports.ErrSCMUnsupported) {
		return StackResult{}, nil
	}
	if err != nil {
		return StackResult{}, fmt.Errorf("read pull request stack: %w", err)
	}
	if inStack {
		view, err := s.stackView(ctx, repo, stack)
		if err != nil {
			return StackResult{}, err
		}
		return StackResult{Stack: &view}, nil
	}
	if tracked.Merged || tracked.Closed {
		return StackResult{}, nil
	}
	parent, ok, err := s.stackParent(ctx, repo, tracked)
	if err != nil {
		return StackResult{}, err
	}
	if !ok {
		return StackResult{}, nil
	}
	return StackResult{Parent: &StackParent{Number: parent.Number, URL: parent.URL}}, nil
}

// stackParent finds the open PR that tracked is based on, as long as a new
// layer can go on it: only the top of an open stack takes one.
func (s *ActionService) stackParent(ctx context.Context, repo ports.SCMRepo, tracked domain.PullRequest) (ports.SCMPRRef, bool, error) {
	target := strings.TrimSpace(tracked.TargetBranch)
	if target == "" {
		return ports.SCMPRRef{}, false, nil
	}
	parent, ok, err := s.stacker.FindOpenPullRequest(ctx, repo, target)
	if err != nil {
		return ports.SCMPRRef{}, false, fmt.Errorf("find parent pull request: %w", err)
	}
	if !ok || parent.Number == tracked.Number {
		return ports.SCMPRRef{}, false, nil
	}
	stack, inStack, err := s.stacker.PullRequestStack(ctx, parent)
	if err != nil {
		return ports.SCMPRRef{}, false, fmt.Errorf("read parent stack: %w", err)
	}
	if inStack && (!stack.Open || len(stack.Layers) == 0 || stack.Layers[len(stack.Layers)-1].Number != parent.Number) {
		return ports.SCMPRRef{}, false, nil
	}
	return parent, true, nil
}

// stackView fills in the title and state of each layer. A layer the provider
// didn't return keeps what the stack itself says about it.
func (s *ActionService) stackView(ctx context.Context, repo ports.SCMRepo, stack ports.SCMStack) (Stack, error) {
	view := Stack{Number: stack.Number, URL: stack.URL, BaseBranch: stack.BaseBranch, Open: stack.Open}
	refs := make([]ports.SCMPRRef, 0, len(stack.Layers))
	for _, layer := range stack.Layers {
		refs = append(refs, ports.SCMPRRef{Repo: repo, Number: layer.Number})
	}
	observed := map[int]ports.SCMObservation{}
	if s.reader != nil {
		observations, err := s.reader.FetchPullRequests(ctx, refs)
		if err != nil {
			return Stack{}, fmt.Errorf("refresh stack layers: %w", err)
		}
		for _, observation := range observations {
			if observation.Fetched {
				observed[observation.PR.Number] = observation
			}
		}
	}
	for _, layer := range stack.Layers {
		out := StackLayer{Number: layer.Number, State: stackLayerState(layer), HeadBranch: layer.HeadBranch, HeadSHA: layer.HeadSHA}
		if observation, ok := observed[layer.Number]; ok {
			out.Title = observation.PR.Title
			out.URL = observation.PR.URL
			out.CI = observation.CI.Summary
			out.NoChecks = s.noChecksSettled(ctx, repo, observation)
			out.ReviewDecision = observation.Review.Decision
			out.Mergeability = observation.Mergeability.State
		}
		if tracked, ok, err := s.store.GetPR(ctx, out.URL); err == nil && ok && out.URL != "" {
			out.SessionID = tracked.SessionID
		}
		view.Layers = append(view.Layers, out)
	}
	return view, nil
}

func stackLayerState(layer ports.SCMStackLayer) domain.PRState {
	switch {
	case layer.Merged:
		return domain.PRStateMerged
	case !layer.Open:
		return domain.PRStateClosed
	case layer.Draft:
		return domain.PRStateDraft
	default:
		return domain.PRStateOpen
	}
}

// AddToStack puts a tracked PR on top of the open PR it is based on.
func (s *ActionService) AddToStack(ctx context.Context, prID, prURL string) (StackResult, error) {
	tracked, repo, err := s.loadStackPR(ctx, prID, prURL)
	if err != nil {
		return StackResult{}, err
	}
	if s.stacker == nil {
		return StackResult{}, ErrUnsupported
	}
	if tracked.Merged || tracked.Closed {
		return StackResult{}, ErrInvalidTransition
	}
	parent, ok, err := s.stackParent(ctx, repo, tracked)
	if err != nil {
		return StackResult{}, err
	}
	if !ok {
		return StackResult{}, fmt.Errorf("%w: no pull request to stack on", ErrInvalidTransition)
	}
	stack, err := s.stacker.RegisterStacked(ctx, ports.SCMStackRegisterRequest{Repo: repo, Parent: parent.Number, Child: tracked.Number})
	if err != nil {
		switch {
		case errors.Is(err, ports.ErrSCMUnsupported):
			return StackResult{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
		case errors.Is(err, ports.ErrSCMRejected):
			return StackResult{}, fmt.Errorf("%w: %w", ErrPRRejected, err)
		default:
			return StackResult{}, fmt.Errorf("add to stack: %w", err)
		}
	}
	view, err := s.stackView(ctx, repo, stack)
	if err != nil {
		return StackResult{}, err
	}
	return StackResult{Stack: &view}, nil
}

// MergeStack re-reads the stack and every layer it would land, then merges them
// together. A stack that moved since the user saw it stops the merge.
func (s *ActionService) MergeStack(ctx context.Context, request StackMergeRequest) (StackMergeResult, error) {
	tracked, repo, err := s.loadStackPR(ctx, request.PRID, request.PRURL)
	if err != nil {
		return StackMergeResult{}, err
	}
	if s.stacker == nil || s.reader == nil {
		return StackMergeResult{}, ErrUnsupported
	}
	method := ports.SCMMergeMethod(strings.ToLower(strings.TrimSpace(request.Method)))
	if method == "" {
		method = ports.SCMMergeSquash
	}
	if !method.Valid() {
		return StackMergeResult{}, fmt.Errorf("%w: unknown merge method %q", ErrInvalidAction, request.Method)
	}
	ref := ports.SCMPRRef{Repo: repo, Number: tracked.Number, URL: tracked.URL}
	stack, inStack, err := s.stacker.PullRequestStack(ctx, ref)
	if err != nil {
		return StackMergeResult{}, fmt.Errorf("read pull request stack: %w", err)
	}
	if !inStack || !stack.Open {
		return StackMergeResult{}, ErrPRHeadChanged
	}
	layers, target, err := layersToLand(stack, tracked.Number, request.ExpectedHeads)
	if err != nil {
		return StackMergeResult{}, err
	}
	numbers := make([]int, 0, len(layers))
	landing := make([]ports.SCMObservation, 0, len(layers))
	for _, layer := range layers {
		fresh, review, err := s.fetchMergeReadiness(ctx, ports.SCMPRRef{Repo: repo, Number: layer.Number})
		if err != nil {
			return StackMergeResult{}, err
		}
		if !strings.EqualFold(fresh.PR.HeadSHA, layer.HeadSHA) {
			return StackMergeResult{}, ErrPRHeadChanged
		}
		if !readyToMerge(fresh, review, request.AllowNoChecks && s.noChecksSettled(ctx, repo, fresh)) {
			return StackMergeResult{}, fmt.Errorf("%w: #%d", ErrPRPreconditions, layer.Number)
		}
		landing = append(landing, fresh)
		numbers = append(numbers, layer.Number)
	}

	status, err := s.stacker.MergeStack(ctx, ports.SCMStackMergeRequest{PR: ref, ExpectedHeadSHA: target.HeadSHA, Method: method})
	if err != nil {
		switch {
		case errors.Is(err, ports.ErrSCMUnsupported):
			return StackMergeResult{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
		case errors.Is(err, ports.ErrSCMNotFound):
			return StackMergeResult{}, fmt.Errorf("%w: %w", ErrPRNotFound, err)
		case errors.Is(err, ports.ErrSCMHeadChanged):
			return StackMergeResult{}, fmt.Errorf("%w: %w", ErrPRHeadChanged, err)
		case errors.Is(err, ports.ErrSCMNotMergeable):
			return StackMergeResult{}, fmt.Errorf("%w: %w", ErrPRNotMergeable, err)
		default:
			return StackMergeResult{}, fmt.Errorf("merge stack: %w", err)
		}
	}
	// The provider has the layers now, so recording them must outlive the
	// request: a slow merge can spend its budget, and a client that gives up
	// must not leave AO without the merge.
	record := context.WithoutCancel(ctx)
	at := s.clock().UTC()
	for _, fresh := range landing {
		s.recordStackLayer(record, fresh, status, at)
	}
	return StackMergeResult{PRNumber: tracked.Number, Numbers: numbers, Method: string(method), Status: string(status), StateChangedAt: at}, nil
}

// layersToLand returns the unmerged layers up to and including number, bottom
// first, once they are all open, not drafts, and at the expected commits.
func layersToLand(stack ports.SCMStack, number int, expected []StackHead) ([]ports.SCMStackLayer, ports.SCMStackLayer, error) {
	var layers []ports.SCMStackLayer
	var target ports.SCMStackLayer
	found := false
	for _, layer := range stack.Layers {
		if !layer.Merged {
			layers = append(layers, layer)
		}
		if layer.Number == number {
			target, found = layer, true
			break
		}
	}
	if !found || target.Merged || len(layers) == 0 {
		return nil, ports.SCMStackLayer{}, ErrPRHeadChanged
	}
	if len(expected) != len(layers) {
		return nil, ports.SCMStackLayer{}, ErrPRHeadChanged
	}
	heads := make(map[int]string, len(expected))
	for _, head := range expected {
		heads[head.Number] = strings.TrimSpace(head.HeadSHA)
	}
	for _, layer := range layers {
		if !strings.EqualFold(heads[layer.Number], layer.HeadSHA) || layer.HeadSHA == "" {
			return nil, ports.SCMStackLayer{}, ErrPRHeadChanged
		}
		if !layer.Open || layer.Draft {
			return nil, ports.SCMStackLayer{}, fmt.Errorf("%w: #%d", ErrPRNotMergeable, layer.Number)
		}
	}
	return layers, target, nil
}

// recordStackLayer brings AO up to date with one layer after the provider took
// the merge. A landed layer is recorded as a plain merge is, lifecycle included,
// from fresh, the observation read before merging; the refresh only fills in
// what the merge left unknown, such as its commit. A queued or pending layer
// stays open, so the observer sees it land; it only needs a sooner look.
func (s *ActionService) recordStackLayer(ctx context.Context, fresh ports.SCMObservation, status ports.SCMStackMergeStatus, at time.Time) {
	if fresh.PR.URL == "" {
		return
	}
	tracked, ok, err := s.store.GetPR(ctx, fresh.PR.URL)
	if err != nil {
		s.logger.Warn("stack merge succeeded but loading the layer failed", "pr", fresh.PR.URL, "err", err)
		return
	}
	if !ok {
		return
	}
	if status == ports.SCMStackMerged {
		s.recordMerge(ctx, tracked, fresh, "", at)
	}
	if s.refresher != nil {
		s.refresher.RequestReviewRefresh(tracked.URL)
	}
}

func (s *ActionService) loadStackPR(ctx context.Context, prID, prURL string) (domain.PullRequest, ports.SCMRepo, error) {
	number, err := parsePRNumber(prID)
	if err != nil || strings.TrimSpace(prURL) == "" {
		return domain.PullRequest{}, ports.SCMRepo{}, fmt.Errorf("%w: invalid pull request identity", ErrInvalidPR)
	}
	tracked, ok, err := s.store.GetPR(ctx, prURL)
	if err != nil {
		return domain.PullRequest{}, ports.SCMRepo{}, fmt.Errorf("load pull request: %w", err)
	}
	if !ok || tracked.Number != number {
		return domain.PullRequest{}, ports.SCMRepo{}, ErrPRNotFound
	}
	repo, ok := scmRepoForPR(tracked)
	if !ok {
		return domain.PullRequest{}, ports.SCMRepo{}, fmt.Errorf("%w: pull request repository is unknown", ErrPRPreconditions)
	}
	return tracked, repo, nil
}

// guardStackedMerge refuses a plain merge of a layer with unmerged layers below
// it, which would land on its parent's branch rather than the stack's base.
func (s *ActionService) guardStackedMerge(ctx context.Context, ref ports.SCMPRRef) error {
	if s.stacker == nil {
		return nil
	}
	stack, inStack, err := s.stacker.PullRequestStack(ctx, ref)
	if err != nil {
		// Not knowing is no reason to block the ordinary path.
		if !errors.Is(err, ports.ErrSCMUnsupported) {
			s.logger.Warn("pull request stack check failed before merge", "pr", ref.URL, "err", err)
		}
		return nil
	}
	if !inStack || !stack.Open {
		return nil
	}
	for _, layer := range stack.Layers {
		if layer.Number == ref.Number {
			return nil
		}
		if !layer.Merged {
			return ErrPRStacked
		}
	}
	return nil
}
