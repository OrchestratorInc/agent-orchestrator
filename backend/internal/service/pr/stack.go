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
	NoChecks       bool
	SessionID      domain.SessionID
}

type Stack struct {
	Number     int
	URL        string
	BaseBranch string
	Open       bool
	Layers     []StackLayer
}

type StackParent struct {
	Number int
	URL    string
}

type StackResult struct {
	Stack  *Stack
	Parent *StackParent
}

type StackHead struct {
	Number  int
	HeadSHA string
}

type StackMergeRequest struct {
	PRID          string
	PRURL         string
	Method        string
	ExpectedHeads []StackHead
	AllowNoChecks bool
}

type StackMergeResult struct {
	PRNumber       int
	Numbers        []int
	Method         string
	Status         string
	StateChangedAt time.Time
}

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
	record := context.WithoutCancel(ctx)
	at := s.clock().UTC()
	for _, fresh := range landing {
		s.recordStackLayer(record, fresh, status, at)
	}
	return StackMergeResult{PRNumber: tracked.Number, Numbers: numbers, Method: string(method), Status: string(status), StateChangedAt: at}, nil
}

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

func (s *ActionService) guardStackedMerge(ctx context.Context, ref ports.SCMPRRef) error {
	if s.stacker == nil {
		return nil
	}
	stack, inStack, err := s.stacker.PullRequestStack(ctx, ref)
	if err != nil {
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
