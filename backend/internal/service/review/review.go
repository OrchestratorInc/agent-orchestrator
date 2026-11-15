package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/reqid"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

var errRunSuperseded = errors.New("review: run is no longer running")

var (
	ErrInvalid              = reviewcore.ErrInvalid
	ErrNotFound             = reviewcore.ErrNotFound
	ErrConflict             = reviewcore.ErrConflict
	ErrReviewAlreadyRunning = reviewcore.ErrReviewAlreadyRunning
	ErrHeadAlreadyReviewed  = reviewcore.ErrHeadAlreadyReviewed
	ErrPROwnedElsewhere     = reviewcore.ErrPROwnedElsewhere
	ErrAgentBinaryNotFound  = ports.ErrAgentBinaryNotFound
)

func reviewErrorKind(err error) string {
	switch {
	case errors.Is(err, reviewcore.ErrInvalid):
		return "invalid"
	case errors.Is(err, reviewcore.ErrNotFound):
		return "not_found"
	case errors.Is(err, reviewcore.ErrConflict):
		return "conflict"
	case errors.Is(err, ports.ErrAgentBinaryNotFound):
		return "agent_unavailable"
	}
	kind, _ := telemetrymeta.ErrorKindAndCode(err)
	return kind
}

type Manager interface {
	RecoverChatReviewers(ctx context.Context) error
	TriggerRequested(ctx context.Context, workerID domain.SessionID, req TriggerRequest) (TriggerOutcome, error)
	RequestRereview(ctx context.Context, workerID domain.SessionID, prURL, reviewer string) error
	ResolveReviewComment(ctx context.Context, workerID domain.SessionID, prURL, commentURL string) error
	TriggerAuto(ctx context.Context, workerID domain.SessionID, harness domain.ReviewerHarness) (reviewcore.TriggerResult, error)
	Cancel(ctx context.Context, workerID domain.SessionID) (reviewcore.CancelResult, error)
	TerminateReviewer(ctx context.Context, workerID domain.SessionID, body string) error
	ArchiveReviewer(ctx context.Context, workerID domain.SessionID) error
	TeardownReviewerTerminal(ctx context.Context, workerID domain.SessionID) error
	RestoreReviewer(ctx context.Context, workerID domain.SessionID) error
	SwitchReviewer(ctx context.Context, workerID domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig) (reviewcore.SessionReviews, error)
	ApplyReviewActivitySignal(ctx context.Context, reviewSessionID string, signal ActivitySignal) error
	Submit(ctx context.Context, workerID domain.SessionID, runID string, verdict domain.ReviewVerdict, body, githubReviewID string) (domain.ReviewRun, error)
	SubmitMany(ctx context.Context, workerID domain.SessionID, reviews []SubmittedReview) ([]domain.ReviewRun, error)
	List(ctx context.Context, workerID domain.SessionID) (reviewcore.SessionReviews, error)
}

type Service struct {
	engine             *reviewcore.Engine
	store              Store
	requester          ports.SCMReviewRequester
	resolver           ports.SCMReviewResolver
	clock              func() time.Time
	telemetry          ports.EventSink
	codexOperationGate ports.CodexOperationGate
	notifications      reviewNotificationSink
	prRefresher        PRRefresher
	reviewRefresher    ThreadRefresher
	engineTrigger      func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error)
}

type reviewNotificationSink interface {
	Notify(context.Context, ports.NotificationIntent) error
}

var _ Manager = (*Service)(nil)

func (s *Service) RecoverChatReviewers(ctx context.Context) error {
	return s.engine.RecoverChatReviewers(ctx)
}

type Store interface {
	GetReviewByID(ctx context.Context, id string) (domain.Review, bool, error)
	UpdateReviewActivity(ctx context.Context, id string, state domain.ActivityState, agentSessionID, launchID string) (bool, error)
	GetReviewRun(ctx context.Context, id string) (domain.ReviewRun, bool, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	SetSessionAutoInjectReview(ctx context.Context, id domain.SessionID, autoInject bool, updatedAt time.Time) (bool, error)
	UpdateReviewRunResult(ctx context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body, githubReviewID string, autoInjectReview bool) (bool, error)
	ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error)
	ListPRReviews(ctx context.Context, prURL string) ([]domain.PullRequestReview, error)
	ListPRComments(ctx context.Context, prURL string) ([]domain.PullRequestComment, error)
	MarkPRReviewThreadResolved(ctx context.Context, prURL, threadID string) error
}

type PRRefresher interface {
	RefreshPR(ctx context.Context, workerID domain.SessionID, prURL string) error
}

type BranchPRFinder interface {
	FindBranchPRs(ctx context.Context, workerID domain.SessionID) (BranchPRs, error)
}

type BranchPRs struct {
	Own         []string
	OtherAuthor []string
}

const prRefreshTimeout = 10 * time.Second

type Option func(*Service)

func WithPRRefresher(r PRRefresher) Option {
	return func(s *Service) { s.prRefresher = r }
}

type ThreadRefresher interface {
	RequestReviewRefresh(prURL string)
}

func WithThreadRefresher(r ThreadRefresher) Option {
	return func(s *Service) { s.reviewRefresher = r }
}

func WithClock(clock func() time.Time) Option {
	return func(s *Service) { s.clock = clock }
}

func WithReviewRequester(requester ports.SCMReviewRequester) Option {
	return func(s *Service) { s.requester = requester }
}

func WithReviewResolver(resolver ports.SCMReviewResolver) Option {
	return func(s *Service) { s.resolver = resolver }
}

func WithTelemetry(sink ports.EventSink) Option {
	return func(s *Service) { s.telemetry = sink }
}

func WithNotificationSink(sink reviewNotificationSink) Option {
	return func(s *Service) { s.notifications = sink }
}

func WithCodexAccountOperationGate(gate ports.CodexOperationGate) Option {
	return func(s *Service) { s.codexOperationGate = gate }
}

func (s *Service) emit(ctx context.Context, name string, sessionID domain.SessionID, payload map[string]any) {
	if s.telemetry == nil {
		return
	}
	session := sessionID
	s.telemetry.Emit(context.Background(), ports.TelemetryEvent{
		Name:       name,
		Source:     "review_service",
		OccurredAt: s.clock(),
		Level:      ports.TelemetryLevelInfo,
		SessionID:  &session,
		RequestID:  reqid.FromContext(ctx),
		Payload:    payload,
	})
}

func New(engine *reviewcore.Engine, store Store, opts ...Option) *Service {
	s := &Service{
		engine: engine,
		store:  store,
		clock:  func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.engineTrigger == nil {
		s.engineTrigger = func(ctx context.Context, workerID domain.SessionID, opts reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
			return s.engine.TriggerWithOptions(ctx, workerID, opts)
		}
	}
	return s
}

func (s *Service) RequestRereview(ctx context.Context, workerID domain.SessionID, prURL, reviewer string) error {
	reviewer = strings.TrimSpace(strings.TrimPrefix(reviewer, "@"))
	if workerID == "" {
		return fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if reviewer == "" {
		return fmt.Errorf("%w: reviewer is required", ErrInvalid)
	}
	if s.requester == nil {
		return fmt.Errorf("%w: review request provider is unavailable", ErrInvalid)
	}
	prs, err := s.store.ListPRsBySession(ctx, workerID)
	if err != nil {
		return err
	}
	if len(prs) == 0 {
		return fmt.Errorf("%w: worker %q has no PR", ErrInvalid, workerID)
	}
	pr, ok := selectRereviewPR(prs, prURL)
	if !ok {
		return fmt.Errorf("%w: pull request is not tracked for worker %q", ErrNotFound, workerID)
	}
	if pr.Closed || pr.Merged {
		return fmt.Errorf("%w: pull request is not open", ErrInvalid)
	}
	if !reviewerReviewedPR(ctx, s.store, pr.URL, reviewer) {
		return fmt.Errorf("%w: reviewer %q has not reviewed this PR", ErrInvalid, reviewer)
	}
	ref, err := reviewRequestRef(pr)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := s.requester.RequestReview(ctx, ports.SCMReviewRequest{PR: ref, Reviewer: reviewer}); err != nil {
		if errors.Is(err, ports.ErrSCMNotFound) {
			return fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		if errors.Is(err, ports.ErrSCMUnsupported) {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		return err
	}
	s.emit(ctx, "ao.review.rereview_requested", workerID, map[string]any{
		"provider": pr.Provider,
	})
	return nil
}

func selectRereviewPR(prs []domain.PullRequest, prURL string) (domain.PullRequest, bool) {
	want := strings.TrimSpace(prURL)
	if want == "" && len(prs) == 1 {
		return prs[0], true
	}
	for _, pr := range prs {
		if pr.URL == want || pr.HTMLURL == want {
			return pr, true
		}
	}
	return domain.PullRequest{}, false
}

func reviewerReviewedPR(ctx context.Context, store Store, prURL, reviewer string) bool {
	reviews, err := store.ListPRReviews(ctx, prURL)
	if err != nil {
		return false
	}
	for _, review := range reviews {
		if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(review.Author), "@"), reviewer) {
			return true
		}
	}
	comments, err := store.ListPRComments(ctx, prURL)
	if err != nil {
		return false
	}
	for _, comment := range comments {
		if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(comment.Author), "@"), reviewer) {
			return true
		}
	}
	return false
}

func reviewRequestRef(pr domain.PullRequest) (ports.SCMPRRef, error) {
	repo := ports.SCMRepo{Provider: pr.Provider, Host: pr.Host, Repo: pr.Repo}
	if repo.Provider == "" {
		repo.Provider = providerFromPRURL(pr.URL)
	}
	if repo.Host == "" {
		repo.Host = hostFromPRURL(pr.URL)
	}
	if repo.Repo != "" {
		parts := strings.SplitN(repo.Repo, "/", 2)
		if len(parts) == 2 {
			repo.Owner = parts[0]
			repo.Name = parts[1]
		}
	}
	if repo.Provider == "github" && (repo.Owner == "" || repo.Name == "") {
		owner, name := githubOwnerRepoFromPRURL(pr.URL)
		repo.Owner, repo.Name = owner, name
		if repo.Repo == "" && owner != "" && name != "" {
			repo.Repo = owner + "/" + name
		}
	}
	if pr.Number <= 0 || repo.Provider == "" || repo.Owner == "" || repo.Name == "" {
		return ports.SCMPRRef{}, fmt.Errorf("invalid pull request reference")
	}
	return ports.SCMPRRef{Repo: repo, Number: pr.Number, URL: pr.URL}, nil
}

func providerFromPRURL(raw string) string {
	if strings.Contains(raw, "/-/merge_requests/") {
		return "gitlab"
	}
	if strings.Contains(raw, "/pull/") {
		return "github"
	}
	return ""
}

func hostFromPRURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func githubOwnerRepoFromPRURL(raw string) (string, string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 4 && parts[2] == "pull" {
		return parts[0], parts[1]
	}
	return "", ""
}

func (s *Service) ResolveReviewComment(ctx context.Context, workerID domain.SessionID, prURL, commentURL string) error {
	commentURL = strings.TrimSpace(commentURL)
	if workerID == "" {
		return fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if commentURL == "" {
		return fmt.Errorf("%w: comment URL is required", ErrInvalid)
	}
	if s.resolver == nil {
		return fmt.Errorf("%w: review resolver provider is unavailable", ErrInvalid)
	}
	prs, err := s.store.ListPRsBySession(ctx, workerID)
	if err != nil {
		return err
	}
	pr, ok := selectRereviewPR(prs, prURL)
	if !ok {
		return fmt.Errorf("%w: pull request is not tracked for worker %q", ErrNotFound, workerID)
	}
	comments, err := s.store.ListPRComments(ctx, pr.URL)
	if err != nil {
		return err
	}
	var target domain.PullRequestComment
	for _, comment := range comments {
		if comment.URL == commentURL {
			target = comment
			break
		}
	}
	if target.ThreadID == "" {
		return fmt.Errorf("%w: review comment is not tracked for this PR", ErrNotFound)
	}
	if target.Resolved {
		return s.store.MarkPRReviewThreadResolved(ctx, pr.URL, target.ThreadID)
	}
	ref, err := reviewRequestRef(pr)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := s.resolver.ResolveReviewThread(ctx, ports.SCMReviewResolveRequest{PR: ref, ThreadID: target.ThreadID}); err != nil {
		if errors.Is(err, ports.ErrSCMNotFound) {
			return fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		if errors.Is(err, ports.ErrSCMUnsupported) {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		return err
	}
	if err := s.store.MarkPRReviewThreadResolved(ctx, pr.URL, target.ThreadID); err != nil {
		return err
	}
	s.emit(ctx, "ao.review.comment_resolved", workerID, map[string]any{"provider": pr.Provider})
	return nil
}

type TriggerRequest struct {
	Harness            domain.ReviewerHarness
	Config             domain.AgentConfig
	Source             domain.ReviewTriggerSource
	InterfaceMode      domain.ReviewerInterfaceMode
	PRURL              string
	RejectReviewedHead bool
	Rerun              bool
	EnableAutoInject   bool
}

type TriggerOutcome struct {
	reviewcore.TriggerResult
	AutoInjectEnabled bool
}

func (s *Service) TriggerRequested(ctx context.Context, workerID domain.SessionID, req TriggerRequest) (TriggerOutcome, error) {
	source := req.Source
	if source == "" {
		source = domain.ReviewTriggerManual
	}
	if source != domain.ReviewTriggerManual && source != domain.ReviewTriggerAgent {
		return TriggerOutcome{}, fmt.Errorf("%w: review trigger source must be %q or %q", ErrInvalid, domain.ReviewTriggerManual, domain.ReviewTriggerAgent)
	}
	if mode := req.InterfaceMode; mode != "" && mode != domain.ReviewerInterfaceChat && mode != domain.ReviewerInterfaceTUI {
		return TriggerOutcome{}, fmt.Errorf("%w: unknown reviewer interface mode %q", ErrInvalid, mode)
	}
	if req.Rerun && req.RejectReviewedHead {
		return TriggerOutcome{}, fmt.Errorf("%w: rerun cannot be combined with rejecting an already-reviewed head", ErrInvalid)
	}
	branchLookupFoundNone, err := s.refreshPRs(ctx, workerID, strings.TrimSpace(req.PRURL))
	if err != nil {
		return TriggerOutcome{}, err
	}
	result, err := s.triggerWithOptions(ctx, workerID, reviewcore.TriggerOptions{
		Harness:                 req.Harness,
		Config:                  req.Config,
		Source:                  source,
		InterfaceMode:           req.InterfaceMode,
		PRURL:                   strings.TrimSpace(req.PRURL),
		RejectReviewedHead:      req.RejectReviewedHead,
		Rerun:                   req.Rerun,
		BranchPRLookupFoundNone: branchLookupFoundNone,
	})
	if err != nil {
		return TriggerOutcome{}, err
	}
	outcome := TriggerOutcome{TriggerResult: result}
	if !req.EnableAutoInject {
		return outcome, nil
	}
	session, ok, err := s.store.GetSession(ctx, workerID)
	if err != nil {
		return TriggerOutcome{}, err
	}
	if !ok {
		return TriggerOutcome{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	if session.AutoInjectReview {
		return outcome, nil
	}
	updated, err := s.store.SetSessionAutoInjectReview(ctx, workerID, true, s.clock())
	if err != nil {
		return TriggerOutcome{}, fmt.Errorf("enable review auto-inject: %w", err)
	}
	outcome.AutoInjectEnabled = updated
	return outcome, nil
}

func (s *Service) refreshPRs(ctx context.Context, workerID domain.SessionID, prURL string) (bool, error) {
	if s.prRefresher == nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, prRefreshTimeout)
	defer cancel()
	tracked, err := s.store.ListPRsBySession(ctx, workerID)
	if err != nil {
		return false, err
	}
	if prURL == "" && !hasOpenPR(tracked) {
		return s.attachBranchPR(ctx, workerID)
	}
	if prURL != "" {
		known := false
		for _, pr := range tracked {
			if pr.URL == prURL || pr.HTMLURL == prURL {
				known = true
				break
			}
		}
		if err := s.prRefresher.RefreshPR(ctx, workerID, prURL); err != nil && !known {
			return false, err
		} else if err != nil {
			slog.Default().WarnContext(ctx, "review trigger: refresh named PR failed; using stored facts", "session", workerID, "err", err)
		}
		return false, nil
	}
	for _, pr := range tracked {
		if pr.Merged || pr.Closed {
			continue
		}
		if err := s.prRefresher.RefreshPR(ctx, workerID, firstNonEmpty(pr.HTMLURL, pr.URL)); err != nil {
			slog.Default().WarnContext(ctx, "review trigger: refresh PR failed; using stored facts", "session", workerID, "err", err)
		}
	}
	return false, nil
}

func hasOpenPR(prs []domain.PullRequest) bool {
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed {
			return true
		}
	}
	return false
}

func (s *Service) attachBranchPR(ctx context.Context, workerID domain.SessionID) (bool, error) {
	finder, ok := s.prRefresher.(BranchPRFinder)
	if !ok {
		return false, nil
	}
	found, err := finder.FindBranchPRs(ctx, workerID)
	if err != nil {
		slog.Default().WarnContext(ctx, "review trigger: find PR on worker branch failed", "session", workerID, "err", err)
		return false, fmt.Errorf("%w: Orchestrator is not tracking an open PR for worker %q yet and could not look up open PRs from its branch (%w); if you just opened one, run `ao review trigger %s --pr <url>` with that PR's URL", ErrInvalid, workerID, err, workerID)
	}
	switch len(found.Own) {
	case 0:
		if len(found.OtherAuthor) > 0 {
			return false, fmt.Errorf("%w: Orchestrator is not tracking an open PR for worker %q yet; found a PR from another author on its branch, which Orchestrator does not attach automatically; to review it anyway, run %s", ErrInvalid, workerID, retryCommands(workerID, found.OtherAuthor))
		}
		return true, nil
	case 1:
		prURL := found.Own[0]
		if err := s.prRefresher.RefreshPR(ctx, workerID, prURL); err != nil {
			return false, fmt.Errorf("found PR %s on worker %q's branch but could not attach it: %w; run `ao review trigger %s --pr %s` to try again", prURL, workerID, err, workerID, prURL)
		}
		return false, nil
	}
	return false, fmt.Errorf("%w: worker %q has %d open PRs from its branch that Orchestrator does not track yet; name one: %s", ErrInvalid, workerID, len(found.Own), retryCommands(workerID, found.Own))
}

func retryCommands(workerID domain.SessionID, urls []string) string {
	cmds := make([]string, len(urls))
	for i, prURL := range urls {
		cmds[i] = fmt.Sprintf("`ao review trigger %s --pr %s`", workerID, prURL)
	}
	return strings.Join(cmds, ", ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (s *Service) TriggerAuto(ctx context.Context, workerID domain.SessionID, harness domain.ReviewerHarness) (reviewcore.TriggerResult, error) {
	return s.triggerWithOptions(ctx, workerID, reviewcore.TriggerOptions{Harness: harness, Source: domain.ReviewTriggerAuto})
}

func (s *Service) triggerWithOptions(
	ctx context.Context,
	workerID domain.SessionID,
	opts reviewcore.TriggerOptions,
) (reviewcore.TriggerResult, error) {
	harness, config, source := opts.Harness, opts.Config, opts.Source
	triggeredPayload := map[string]any{"trigger": string(source), "rerun": opts.Rerun}
	if err := config.Validate(); err != nil {
		err = fmt.Errorf("%w: reviewer config: %w", ErrInvalid, err)
		s.emit(ctx, "ao.reviews.trigger_failed", workerID, map[string]any{
			"error_kind": reviewErrorKind(err),
			"trigger":    string(source),
		})
		return reviewcore.TriggerResult{}, err
	}
	usesCodex := s.codexReviewUsesCodex(ctx, workerID, harness)
	var release func()
	if usesCodex && s.codexOperationGate != nil {
		var err error
		release, err = s.codexOperationGate.AcquireSharedWait(ctx)
		if err != nil {
			return reviewcore.TriggerResult{}, err
		}
		defer release()
	}
	result, err := s.engineTrigger(ctx, workerID, opts)
	if err != nil {
		s.emit(ctx, "ao.reviews.trigger_failed", workerID, map[string]any{
			"error_kind": reviewErrorKind(err),
			"trigger":    string(source),
		})
		return result, err
	}
	if result.Run.Harness != "" {
		triggeredPayload["harness"] = string(result.Run.Harness)
	}
	createdOrRestarted := result.Created || len(result.CreatedRuns) > 0
	if !createdOrRestarted {
		return result, nil
	}
	triggeredPayload["created_runs"] = len(result.CreatedRuns)
	s.emit(ctx, "ao.reviews.triggered", workerID, triggeredPayload)
	return result, nil
}

func (s *Service) Cancel(ctx context.Context, workerID domain.SessionID) (reviewcore.CancelResult, error) {
	result, err := s.engine.Cancel(ctx, workerID)
	if err != nil {
		return result, err
	}
	s.emit(ctx, "ao.reviews.cancelled", workerID, map[string]any{
		"cancelled_runs": len(result.CancelledRuns),
	})
	return result, nil
}

func (s *Service) TerminateReviewer(ctx context.Context, workerID domain.SessionID, body string) error {
	_, err := s.engine.TerminateReviewer(ctx, workerID, body)
	return err
}

func (s *Service) TeardownReviewerTerminal(ctx context.Context, workerID domain.SessionID) error {
	return s.engine.TeardownReviewerTerminal(ctx, workerID)
}

func (s *Service) RestoreReviewer(ctx context.Context, workerID domain.SessionID) error {
	release, err := s.acquireReviewerCodexAdmission(ctx, workerID, "")
	if err != nil {
		return err
	}
	defer release()
	_, err = s.engine.RestoreReviewer(ctx, workerID)
	return err
}

func (s *Service) SwitchReviewer(ctx context.Context, workerID domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig) (reviewcore.SessionReviews, error) {
	release, err := s.acquireReviewerCodexAdmission(ctx, workerID, harness)
	if err != nil {
		return reviewcore.SessionReviews{}, err
	}
	defer release()
	return s.engine.SwitchReviewer(ctx, workerID, harness, config)
}

func (s *Service) codexReviewUsesCodex(ctx context.Context, workerID domain.SessionID, harness domain.ReviewerHarness) bool {
	if harness == domain.ReviewerCodex {
		return true
	}
	if harness != "" {
		return false
	}
	rec, ok, err := s.store.GetSession(ctx, workerID)
	return err == nil && ok && (rec.Harness == domain.HarnessCodex || rec.ReviewerHarness == domain.ReviewerCodex)
}

func (s *Service) acquireReviewerCodexAdmission(ctx context.Context, workerID domain.SessionID, harness domain.ReviewerHarness) (func(), error) {
	if s.codexOperationGate == nil || !s.codexReviewUsesCodex(ctx, workerID, harness) {
		return func() {}, nil
	}
	return s.codexOperationGate.AcquireSharedWait(ctx)
}

type ActivitySignal struct {
	Event          string
	State          domain.ActivityState
	AgentSessionID string
	LaunchID       string
}

func (s *Service) ApplyReviewActivitySignal(ctx context.Context, reviewSessionID string, signal ActivitySignal) error {
	if reviewSessionID == "" {
		return fmt.Errorf("%w: review session id is required", ErrInvalid)
	}
	review, ok, err := s.store.GetReviewByID(ctx, reviewSessionID)
	if err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: review session %q", ErrNotFound, reviewSessionID)
	}
	if signal.AgentSessionID == "" && signal.State == "" {
		return nil
	}
	updated, err := s.store.UpdateReviewActivity(ctx, reviewSessionID, signal.State, signal.AgentSessionID, signal.LaunchID)
	if err != nil {
		return err
	}
	if !updated {
		if review.ReviewerLaunchID != "" && signal.LaunchID != review.ReviewerLaunchID {
			return nil
		}
		return fmt.Errorf("%w: review session %q", ErrNotFound, reviewSessionID)
	}
	return nil
}

type SubmittedReview struct {
	RunID          string
	Verdict        domain.ReviewVerdict
	Body           string
	GithubReviewID string
}

func (s *Service) Submit(ctx context.Context, workerID domain.SessionID, runID string, verdict domain.ReviewVerdict, body, githubReviewID string) (domain.ReviewRun, error) {
	runs, err := s.SubmitMany(ctx, workerID, []SubmittedReview{{
		RunID:          runID,
		Verdict:        verdict,
		Body:           body,
		GithubReviewID: githubReviewID,
	}})
	if err != nil {
		return domain.ReviewRun{}, err
	}
	if len(runs) == 0 {
		return domain.ReviewRun{}, fmt.Errorf("%w: no review result submitted", ErrInvalid)
	}
	return runs[0], nil
}

func (s *Service) SubmitMany(ctx context.Context, workerID domain.SessionID, reviews []SubmittedReview) ([]domain.ReviewRun, error) {
	if workerID == "" {
		return nil, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if len(reviews) == 0 {
		return nil, fmt.Errorf("%w: at least one review result is required", ErrInvalid)
	}
	if s.store == nil {
		return nil, fmt.Errorf("review service store is not configured")
	}
	runs := make([]domain.ReviewRun, 0, len(reviews))
	var supersededRunIDs []string
	for _, review := range reviews {
		run, err := s.submitOne(ctx, workerID, review)
		if err != nil {
			if errors.Is(err, errRunSuperseded) {
				supersededRunIDs = append(supersededRunIDs, review.RunID)
				continue
			}
			return nil, err
		}
		runs = append(runs, run)
	}
	if len(runs) == 0 {
		if len(supersededRunIDs) > 0 {
			return nil, fmt.Errorf("%w: no submittable review runs in submission (superseded: %s)", ErrInvalid, strings.Join(supersededRunIDs, ", "))
		}
		return nil, fmt.Errorf("%w: no submittable review runs in submission", ErrInvalid)
	}
	return runs, nil
}

func (s *Service) submitOne(ctx context.Context, workerID domain.SessionID, review SubmittedReview) (domain.ReviewRun, error) {
	runID := review.RunID
	verdict := review.Verdict
	body := review.Body
	githubReviewID := review.GithubReviewID
	if runID == "" {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run id is required", ErrInvalid)
	}
	if !verdict.Valid() {
		return domain.ReviewRun{}, fmt.Errorf("%w: verdict must be %q or %q", ErrInvalid, domain.VerdictApproved, domain.VerdictChangesRequested)
	}
	if verdict == domain.VerdictChangesRequested && body == "" {
		return domain.ReviewRun{}, fmt.Errorf("%w: a changes_requested review requires a body", ErrInvalid)
	}
	run, ok, err := s.store.GetReviewRun(ctx, runID)
	if err != nil {
		return domain.ReviewRun{}, err
	}
	if !ok {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run %q", ErrNotFound, runID)
	}
	if run.SessionID != workerID {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run %q does not belong to worker %q", ErrInvalid, runID, workerID)
	}

	switch run.Status {
	case domain.ReviewRunRunning:
		session, found, err := s.store.GetSession(ctx, workerID)
		if err != nil {
			return domain.ReviewRun{}, err
		}
		if !found {
			return domain.ReviewRun{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
		}
		updated, err := s.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunComplete, verdict, body, githubReviewID, session.AutoInjectReview)
		if err != nil {
			return domain.ReviewRun{}, err
		}
		if !updated {
			return domain.ReviewRun{}, fmt.Errorf("%w: review run %q is not running", errRunSuperseded, runID)
		}
		run.Status = domain.ReviewRunComplete
		run.Verdict = verdict
		run.Body = body
		run.GithubReviewID = githubReviewID
		run.AutoInjectReview = session.AutoInjectReview
		if githubReviewID != "" && s.reviewRefresher != nil {
			s.reviewRefresher.RequestReviewRefresh(run.PRURL)
		}
		s.emit(ctx, "ao.reviews.submitted", workerID, map[string]any{
			"harness":            string(run.Harness),
			"verdict":            string(verdict),
			"duration_ms":        s.clock().Sub(run.CreatedAt).Milliseconds(),
			"posted_to_provider": githubReviewID != "",
			"trigger":            string(run.TriggerSource),
			"body_bytes":         len(body),
			"auto_inject":        session.AutoInjectReview,
		})
	case domain.ReviewRunComplete:
		if run.Verdict != verdict {
			return domain.ReviewRun{}, fmt.Errorf("%w: review run %q already recorded verdict %q", ErrInvalid, runID, run.Verdict)
		}
		if body != "" && body != run.Body {
			return domain.ReviewRun{}, fmt.Errorf("%w: review run %q already recorded a different body", ErrInvalid, runID)
		}
		if githubReviewID != "" && githubReviewID != run.GithubReviewID {
			return domain.ReviewRun{}, fmt.Errorf("%w: review run %q already recorded GitHub review id %q", ErrInvalid, runID, run.GithubReviewID)
		}
	case domain.ReviewRunDelivered:
		return run, nil
	default:
		return domain.ReviewRun{}, fmt.Errorf("%w: review run %q is not running", errRunSuperseded, runID)
	}
	s.emitReviewNotification(ctx, run)
	return run, nil
}

func (s *Service) emitReviewNotification(ctx context.Context, run domain.ReviewRun) {
	if s.notifications == nil {
		return
	}
	session, ok, err := s.store.GetSession(ctx, run.SessionID)
	if err != nil || !ok {
		slog.Default().WarnContext(ctx, "review notification session lookup failed", "session", run.SessionID, "run", run.ID, "err", err)
		return
	}
	intent := ports.NotificationIntent{
		SessionID: session.ID, ProjectID: session.ProjectID, PRURL: run.PRURL,
		SessionDisplayName: session.DisplayName, CreatedAt: s.clock(), SourceKey: "review_run:" + run.ID,
	}
	if run.Verdict == domain.VerdictChangesRequested {
		intent.Type = domain.NotificationReviewChangesRequested
	} else {
		intent.Type = domain.NotificationReviewCompleted
	}
	prs, listErr := s.store.ListPRsBySession(ctx, run.SessionID)
	if listErr == nil {
		for _, pr := range prs {
			if pr.URL == run.PRURL || pr.HTMLURL == run.PRURL {
				intent.PRNumber, intent.PRTitle = pr.Number, pr.Title
				break
			}
		}
	}
	if err := s.notifications.Notify(ctx, intent); err != nil {
		slog.Default().WarnContext(ctx, "review notification failed", "session", run.SessionID, "run", run.ID, "err", err)
	}
}

func (s *Service) List(ctx context.Context, workerID domain.SessionID) (reviewcore.SessionReviews, error) {
	return s.engine.List(ctx, workerID)
}

func (s *Service) ArchiveReviewer(ctx context.Context, workerID domain.SessionID) error {
	return s.engine.ArchiveReviewer(ctx, workerID)
}
