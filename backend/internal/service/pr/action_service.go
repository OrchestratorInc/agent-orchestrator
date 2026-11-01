package pr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	prNumberPattern = regexp.MustCompile(`^[1-9]\d*$`)
	gitSHAPattern   = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)
)

type actionStore interface {
	GetPR(ctx context.Context, url string) (domain.PullRequest, bool, error)
}

type resolveStore interface {
	actionStore
	GetPRByNumber(ctx context.Context, number int) (domain.PullRequest, bool, error)
	ListChecks(ctx context.Context, prURL string) ([]domain.PullRequestCheck, error)
	ListPRComments(ctx context.Context, prURL string) ([]domain.PullRequestComment, error)
	ListPRReviewThreads(ctx context.Context, prURL string) ([]domain.PullRequestReviewThread, error)
	ListPRReviews(ctx context.Context, prURL string) ([]domain.PullRequestReview, error)
}

type resolveNumberCounter interface {
	CountActivePRsByNumber(context.Context, int) (int, error)
}

type actionReader interface {
	FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error)
	FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error)
}

type actionLifecycle interface {
	ApplySCMObservation(ctx context.Context, sessionID domain.SessionID, obs ports.SCMObservation) error
}

type mergeStateWriter interface {
	MarkPRMerged(ctx context.Context, prURL, mergeCommitSHA string, mergedAt time.Time) error
}

// StateWriter records a provider-confirmed PR state change locally.
type StateWriter interface {
	MarkPRState(ctx context.Context, prURL string, state domain.PRState, changedAt time.Time) error
}

// CommentWriter records a just-posted comment locally so the PR summary shows
// it before the next SCM observation reconciles it.
type CommentWriter interface {
	UpsertPRReviewComment(ctx context.Context, prURL string, comment domain.PullRequestComment) error
	AppendPRConversationComment(ctx context.Context, prURL string, comment domain.PullRequestConversationComment) (bool, error)
}

// ReviewRefresher asks the SCM observer to re-fetch one PR's metadata and
// review threads now without waiting for its regular cadence.
type ReviewRefresher interface {
	RequestReviewRefresh(prURL string)
}

// ActionDeps contains the storage and SCM boundaries used by ActionService.
type ActionDeps struct {
	Store        actionStore
	ResolveStore resolveStore
	Merger       ports.SCMMerger
	Reader       actionReader
	Resolver     ports.SCMReviewResolver
	Writer       ports.SCMWriter
	MergeWriter  mergeStateWriter
	Lifecycle    actionLifecycle
	Clock        func() time.Time
	Logger       *slog.Logger
	// ThreadWriter updates one resolved thread and its comments without
	// replacing a potentially newer SCM observation.
	ThreadWriter interface {
		MarkPRReviewThreadResolved(context.Context, string, string) error
	}
	StateChanger  ports.SCMPRStateChanger
	StateWriter   StateWriter
	Commenter     ports.SCMCommenter
	CommentWriter CommentWriter
	Refresher     ReviewRefresher
}

// ActionService validates current pull request state before applying mutations.
type ActionService struct {
	store        actionStore
	resolve      resolveStore
	merger       ports.SCMMerger
	reader       actionReader
	resolver     ports.SCMReviewResolver
	mergeWriter  mergeStateWriter
	lifecycle    actionLifecycle
	clock        func() time.Time
	logger       *slog.Logger
	threadWriter interface {
		MarkPRReviewThreadResolved(context.Context, string, string) error
	}
	stateChanger  ports.SCMPRStateChanger
	stateWriter   StateWriter
	commenter     ports.SCMCommenter
	commentWriter CommentWriter
	refresher     ReviewRefresher
}

var _ ActionManager = (*ActionService)(nil)

// NewActionService builds the guarded pull request action service.
func NewActionService(deps ActionDeps) *ActionService {
	resolve := deps.ResolveStore
	if resolve == nil {
		resolve, _ = deps.Store.(resolveStore)
	}
	threadWriter := deps.ThreadWriter
	if threadWriter == nil {
		threadWriter, _ = deps.Writer.(interface {
			MarkPRReviewThreadResolved(context.Context, string, string) error
		})
	}
	clock := deps.Clock
	if clock == nil {
		clock = time.Now
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ActionService{
		store:         deps.Store,
		resolve:       resolve,
		merger:        deps.Merger,
		reader:        deps.Reader,
		resolver:      deps.Resolver,
		mergeWriter:   deps.MergeWriter,
		lifecycle:     deps.Lifecycle,
		clock:         clock,
		logger:        logger,
		threadWriter:  threadWriter,
		stateChanger:  deps.StateChanger,
		stateWriter:   deps.StateWriter,
		commenter:     deps.Commenter,
		commentWriter: deps.CommentWriter,
		refresher:     deps.Refresher,
	}
}

// Merge re-fetches authoritative SCM state and then merges (squash by default,
// or the requested merge/rebase method) only the exact head the user saw. The
// provider repeats the SHA guard atomically.
func (s *ActionService) Merge(ctx context.Context, request MergeRequest) (MergeResult, error) {
	prNumber, err := parsePRNumber(request.PRID)
	if err != nil || strings.TrimSpace(request.PRURL) == "" {
		return MergeResult{}, fmt.Errorf("%w: invalid pull request identity", ErrInvalidPR)
	}
	if s.store == nil || s.merger == nil || s.reader == nil {
		return MergeResult{}, errors.New("pr: merge action is not configured")
	}
	expectedHead := strings.ToLower(strings.TrimSpace(request.ExpectedHeadSHA))
	if !gitSHAPattern.MatchString(expectedHead) {
		return MergeResult{}, fmt.Errorf("%w: invalid expected head", ErrInvalidPR)
	}
	method := ports.SCMMergeMethod(strings.ToLower(strings.TrimSpace(request.Method)))
	if method == "" {
		method = ports.SCMMergeSquash
	}
	if !method.Valid() {
		return MergeResult{}, fmt.Errorf("%w: unknown merge method %q", ErrInvalidAction, request.Method)
	}

	tracked, ok, err := s.store.GetPR(ctx, request.PRURL)
	if err != nil {
		return MergeResult{}, fmt.Errorf("load pull request: %w", err)
	}
	if !ok || tracked.Number != prNumber {
		return MergeResult{}, ErrPRNotFound
	}
	if tracked.Draft || tracked.Merged || tracked.Closed {
		return MergeResult{}, ErrPRNotMergeable
	}
	if !gitSHAPattern.MatchString(strings.TrimSpace(tracked.HeadSHA)) {
		return MergeResult{}, fmt.Errorf("%w: pull request head is unknown", ErrPRPreconditions)
	}
	if !strings.EqualFold(expectedHead, tracked.HeadSHA) {
		return MergeResult{}, ErrPRHeadChanged
	}

	repo, ok := scmRepoForPR(tracked)
	if !ok {
		return MergeResult{}, fmt.Errorf("%w: pull request repository is unknown", ErrPRPreconditions)
	}
	ref := ports.SCMPRRef{Repo: repo, Number: tracked.Number, URL: tracked.URL}
	fresh, review, err := s.fetchMergeReadiness(ctx, ref)
	if err != nil {
		return MergeResult{}, err
	}
	if !strings.EqualFold(fresh.PR.HeadSHA, expectedHead) {
		return MergeResult{}, ErrPRHeadChanged
	}
	if !readyToMerge(fresh, review) {
		return MergeResult{}, ErrPRPreconditions
	}

	out, err := s.merger.MergePullRequest(ctx, ports.SCMMergeRequest{PR: ref, ExpectedHeadSHA: expectedHead, Method: method})
	if err != nil {
		switch {
		case errors.Is(err, ports.ErrSCMUnsupported):
			return MergeResult{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
		case errors.Is(err, ports.ErrSCMNotFound):
			return MergeResult{}, fmt.Errorf("%w: %w", ErrPRNotFound, err)
		case errors.Is(err, ports.ErrSCMHeadChanged):
			return MergeResult{}, fmt.Errorf("%w: %w", ErrPRHeadChanged, err)
		case errors.Is(err, ports.ErrSCMNotMergeable):
			return MergeResult{}, fmt.Errorf("%w: %w", ErrPRNotMergeable, err)
		default:
			return MergeResult{}, fmt.Errorf("merge pull request: %w", err)
		}
	}
	mergedAt := s.clock().UTC()
	persisted := false
	if s.mergeWriter != nil {
		if err := s.mergeWriter.MarkPRMerged(ctx, tracked.URL, out.MergeCommitSHA, mergedAt); err != nil {
			// GitHub has already committed the irreversible remote mutation. Keep
			// returning the authoritative success so callers do not retry a merge
			// that already happened; the observer will reconcile the durable row.
			s.logger.Error("pr merge succeeded but local state persistence failed", "pr", tracked.URL, "err", err)
		} else {
			persisted = true
		}
	}
	if persisted && s.lifecycle != nil {
		fresh.PR.Draft = false
		fresh.PR.Merged = true
		fresh.PR.Closed = false
		fresh.PR.State = string(domain.PRStateMerged)
		fresh.PR.MergeCommitSHA = out.MergeCommitSHA
		fresh.ObservedAt = mergedAt
		if err := s.lifecycle.ApplySCMObservation(ctx, tracked.SessionID, fresh); err != nil {
			s.logger.Error("pr merge succeeded but lifecycle notification failed", "session", tracked.SessionID, "pr", tracked.URL, "err", err)
		}
	}
	return MergeResult{
		PRNumber:       tracked.Number,
		Method:         string(method),
		State:          domain.PRStateMerged,
		MergeCommitSHA: out.MergeCommitSHA,
		StateChangedAt: mergedAt,
	}, nil
}

func (s *ActionService) fetchMergeReadiness(ctx context.Context, ref ports.SCMPRRef) (ports.SCMObservation, ports.SCMReviewObservation, error) {
	observations, err := s.reader.FetchPullRequests(ctx, []ports.SCMPRRef{ref})
	if err != nil {
		if errors.Is(err, ports.ErrSCMNotFound) {
			return ports.SCMObservation{}, ports.SCMReviewObservation{}, fmt.Errorf("%w: %w", ErrPRNotFound, err)
		}
		return ports.SCMObservation{}, ports.SCMReviewObservation{}, fmt.Errorf("refresh pull request before merge: %w", err)
	}
	if len(observations) != 1 || !observations[0].Fetched || observations[0].PR.Number != ref.Number {
		return ports.SCMObservation{}, ports.SCMReviewObservation{}, ErrPRNotFound
	}
	review, err := s.reader.FetchReviewThreads(ctx, ref)
	if err != nil {
		if errors.Is(err, ports.ErrSCMNotFound) {
			return ports.SCMObservation{}, ports.SCMReviewObservation{}, fmt.Errorf("%w: %w", ErrPRNotFound, err)
		}
		return ports.SCMObservation{}, ports.SCMReviewObservation{}, fmt.Errorf("refresh pull request reviews before merge: %w", err)
	}
	return observations[0], review, nil
}

func readyToMerge(o ports.SCMObservation, review ports.SCMReviewObservation) bool {
	if o.PR.HeadSHA == "" || o.CI.HeadSHA != o.PR.HeadSHA || review.Partial {
		return false
	}
	return domain.MergeReadiness{
		Draft:              o.PR.Draft,
		Merged:             o.PR.Merged,
		Closed:             o.PR.Closed,
		CI:                 domain.CIState(o.CI.Summary),
		Review:             domain.ReviewDecision(review.Decision),
		Mergeability:       domain.Mergeability(o.Mergeability.State),
		UnresolvedComments: hasUnresolvedHumanComments(review.Threads),
	}.ReadyToMerge()
}

func hasUnresolvedHumanComments(threads []ports.SCMReviewThreadObservation) bool {
	for _, thread := range threads {
		if thread.Resolved {
			continue
		}
		for _, comment := range thread.Comments {
			if !comment.IsBot {
				return true
			}
		}
	}
	return false
}

func parsePRNumber(value string) (int, error) {
	if !prNumberPattern.MatchString(value) {
		return 0, ErrInvalidPR
	}
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n <= 0 {
		return 0, ErrInvalidPR
	}
	return int(n), nil
}

func scmRepoForPR(pr domain.PullRequest) (ports.SCMRepo, bool) {
	parts := strings.Split(pr.Repo, "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[len(parts)-1]) == "" {
		return ports.SCMRepo{}, false
	}
	provider := strings.ToLower(strings.TrimSpace(pr.Provider))
	if provider == "" {
		provider = "github"
	}
	host := strings.ToLower(strings.TrimSpace(pr.Host))
	if host == "" && provider == "github" {
		host = "github.com"
	}
	return ports.SCMRepo{
		Provider: provider,
		Host:     host,
		Owner:    strings.Join(parts[:len(parts)-1], "/"),
		Name:     parts[len(parts)-1],
		Repo:     pr.Repo,
	}, true
}

// ResolveComments resolves provider review threads first and then marks each
// successful mutation locally. Empty commentIDs resolves every unresolved
// thread returned by the provider. Supplied IDs may be either provider thread
// IDs or comment IDs; comment IDs are mapped to their owning thread before the
// mutation. Resolve-all requires a complete provider listing; an explicit ID
// may still be used when it is present in a partial listing, while unknown IDs
// are rejected because the response cannot prove their membership.
func (s *ActionService) ResolveComments(ctx context.Context, prID string, commentIDs []string) (ResolveResult, error) {
	if s.resolve == nil || s.reader == nil || s.resolver == nil || s.threadWriter == nil {
		return ResolveResult{}, errors.New("pr: resolve-comments action is not configured")
	}
	pr, err := s.lookupResolvePR(ctx, prID)
	if err != nil {
		return ResolveResult{}, err
	}

	repo, ok := scmRepoForPR(pr)
	if !ok {
		return ResolveResult{}, fmt.Errorf("%w: pull request repository is unknown", ErrPRPreconditions)
	}
	ref := ports.SCMPRRef{Repo: repo, Number: pr.Number, URL: pr.URL}

	// Refresh the provider view before mutating anything. This both supplies the
	// resolve-all set and confirms that the tracked number still exists remotely.
	review, err := s.reader.FetchReviewThreads(ctx, ref)
	if err != nil {
		if errors.Is(err, ports.ErrSCMNotFound) {
			return ResolveResult{}, fmt.Errorf("%w: %w", ErrPRNotFound, err)
		}
		return ResolveResult{}, fmt.Errorf("refresh pull request reviews before resolving comments: %w", err)
	}
	if review.Partial && len(commentIDs) == 0 {
		return ResolveResult{}, fmt.Errorf("%w: review thread listing is incomplete; provide an explicit thread or comment id", ErrPRPreconditions)
	}

	threadIDs, err := requestedThreadIDs(review, commentIDs)
	if err != nil {
		return ResolveResult{}, err
	}
	if len(commentIDs) == 0 {
		threadIDs = make([]string, 0, len(review.Threads))
		for _, thread := range review.Threads {
			if thread.Resolved {
				continue
			}
			if id := strings.TrimSpace(thread.ID); id != "" {
				threadIDs = append(threadIDs, id)
			}
		}
		threadIDs = normalizeThreadIDs(threadIDs)
	}
	if len(threadIDs) == 0 {
		return ResolveResult{}, ErrNothingToResolve
	}

	resolved := 0
	for _, id := range threadIDs {
		if err := s.resolver.ResolveReviewThread(ctx, ports.SCMReviewResolveRequest{PR: ref, ThreadID: id}); err != nil {
			mapped := mapResolveError(err)
			return ResolveResult{Resolved: resolved}, mapped
		}
		if err := s.threadWriter.MarkPRReviewThreadResolved(ctx, pr.URL, id); err != nil {
			return ResolveResult{Resolved: resolved}, fmt.Errorf("persist resolved review thread %q: %w", id, err)
		}
		resolved++
	}
	return ResolveResult{Resolved: resolved}, nil
}

func (s *ActionService) lookupResolvePR(ctx context.Context, prID string) (domain.PullRequest, error) {
	number, err := parsePRNumber(strings.TrimSpace(prID))
	if err != nil {
		return domain.PullRequest{}, fmt.Errorf("%w: invalid pull request identity", ErrInvalidPR)
	}
	pr, ok, err := s.resolve.GetPRByNumber(ctx, number)
	if err != nil {
		return domain.PullRequest{}, fmt.Errorf("load pull request: %w", err)
	}
	if counter, ok := s.resolve.(resolveNumberCounter); ok {
		active, err := counter.CountActivePRsByNumber(ctx, number)
		if err != nil {
			return domain.PullRequest{}, fmt.Errorf("check pull request identity: %w", err)
		}
		if active > 1 {
			return domain.PullRequest{}, fmt.Errorf("%w: pull request number %d is tracked in multiple active repositories", ErrPRPreconditions, number)
		}
	}
	if !ok {
		return domain.PullRequest{}, ErrPRNotFound
	}
	return pr, nil
}

func normalizeThreadIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// requestedThreadIDs validates explicit IDs against the provider observation
// and maps documented comment IDs to their owning thread IDs.
func requestedThreadIDs(review ports.SCMReviewObservation, commentIDs []string) ([]string, error) {
	if len(commentIDs) == 0 {
		return nil, nil
	}
	threadIDs := make(map[string]struct{}, len(review.Threads))
	commentToThread := make(map[string]string)
	for _, thread := range review.Threads {
		threadID := strings.TrimSpace(thread.ID)
		if threadID == "" {
			continue
		}
		threadIDs[threadID] = struct{}{}
		for _, comment := range thread.Comments {
			if commentID := strings.TrimSpace(comment.ID); commentID != "" {
				commentToThread[commentID] = threadID
			}
		}
	}

	requested := normalizeThreadIDs(commentIDs)
	out := make([]string, 0, len(requested))
	for _, id := range requested {
		if _, ok := threadIDs[id]; ok {
			out = append(out, id)
			continue
		}
		if threadID, ok := commentToThread[id]; ok {
			out = append(out, threadID)
			continue
		}
		return nil, fmt.Errorf("%w: review thread or comment %q is not part of pull request", ErrPRPreconditions, id)
	}
	return normalizeThreadIDs(out), nil
}

func mapResolveError(err error) error {
	if errors.Is(err, ports.ErrSCMNotFound) {
		return fmt.Errorf("%w: %w", ErrPRNotFound, err)
	}
	return fmt.Errorf("resolve review thread: %w", err)
}

// maxCommentBodyBytes mirrors GitHub's 65,536-character comment limit as a
// byte bound so oversized bodies fail fast with a 400 instead of a provider 422.
const maxCommentBodyBytes = 65536

// ChangeState closes, reopens, marks ready, or converts to draft a tracked PR.
// The transition is checked against AO's stored state first; the provider is
// the final authority. On success the stored state is updated immediately and
// lifecycle receives a fresh observation, as Merge does.
func (s *ActionService) ChangeState(ctx context.Context, request StateRequest) (StateResult, error) {
	prNumber, err := parsePRNumber(request.PRID)
	if err != nil || strings.TrimSpace(request.PRURL) == "" {
		return StateResult{}, fmt.Errorf("%w: invalid pull request identity", ErrInvalidPR)
	}
	action := ports.SCMPRStateAction(strings.ToLower(strings.TrimSpace(request.Action)))
	if !action.Valid() {
		return StateResult{}, fmt.Errorf("%w: unknown state action %q", ErrInvalidAction, request.Action)
	}
	if s.store == nil || s.stateChanger == nil {
		return StateResult{}, errors.New("pr: state action is not configured")
	}
	tracked, ok, err := s.store.GetPR(ctx, request.PRURL)
	if err != nil {
		return StateResult{}, fmt.Errorf("load pull request: %w", err)
	}
	if !ok || tracked.Number != prNumber {
		return StateResult{}, ErrPRNotFound
	}
	target, err := stateTransitionTarget(tracked, action)
	if err != nil {
		return StateResult{}, err
	}
	repo, ok := scmRepoForPR(tracked)
	if !ok {
		return StateResult{}, fmt.Errorf("%w: pull request repository is unknown", ErrPRPreconditions)
	}
	ref := ports.SCMPRRef{Repo: repo, Number: tracked.Number, URL: tracked.URL}
	scmRequest := ports.SCMPRStateRequest{PR: ref, Action: action}
	if repo.Provider == "github" {
		scmRequest.NodeID = strings.TrimSpace(tracked.ProviderID)
	}
	if err := s.stateChanger.ChangePullRequestState(ctx, scmRequest); err != nil {
		return StateResult{}, mapMutationError("change pull request state", err)
	}

	changedAt := s.clock().UTC()
	persisted := false
	if s.stateWriter != nil {
		if err := s.stateWriter.MarkPRState(ctx, tracked.URL, target, changedAt); err != nil {
			// The provider already applied the change; report its success and let
			// the observer reconcile the durable row.
			s.logger.Error("pr state change succeeded but local state persistence failed", "pr", tracked.URL, "state", target, "err", err)
		} else {
			persisted = true
		}
	}
	if persisted {
		s.notifyStateChange(ctx, tracked, ref, target, changedAt)
	}
	return StateResult{PRNumber: tracked.Number, State: target, StateChangedAt: changedAt}, nil
}

// notifyStateChange hands lifecycle a fresh observation of the new state. A
// closed PR is not polled again, so this is the only time lifecycle sees the
// close; failures are logged because the provider mutation already happened.
func (s *ActionService) notifyStateChange(ctx context.Context, tracked domain.PullRequest, ref ports.SCMPRRef, target domain.PRState, changedAt time.Time) {
	if s.lifecycle == nil || s.reader == nil {
		return
	}
	observations, err := s.reader.FetchPullRequests(ctx, []ports.SCMPRRef{ref})
	if err != nil || len(observations) != 1 || !observations[0].Fetched {
		s.logger.Warn("pr state change succeeded but refresh for lifecycle failed", "pr", tracked.URL, "err", err)
		return
	}
	fresh := observations[0]
	// The provider may answer the read before the mutation is visible; the
	// mutation's confirmed result is authoritative for the state fields.
	fresh.PR.Draft = target == domain.PRStateDraft
	fresh.PR.Closed = target == domain.PRStateClosed
	fresh.PR.Merged = false
	fresh.PR.State = string(target)
	fresh.ObservedAt = changedAt
	if err := s.lifecycle.ApplySCMObservation(ctx, tracked.SessionID, fresh); err != nil {
		s.logger.Error("pr state change succeeded but lifecycle notification failed", "session", tracked.SessionID, "pr", tracked.URL, "err", err)
	}
}

// stateTransitionTarget validates action against the stored PR state and
// returns the state the PR moves to.
func stateTransitionTarget(pr domain.PullRequest, action ports.SCMPRStateAction) (domain.PRState, error) {
	if pr.Merged {
		return "", fmt.Errorf("%w: pull request is merged", ErrInvalidTransition)
	}
	switch action {
	case ports.SCMPRClose:
		if pr.Closed {
			return "", fmt.Errorf("%w: pull request is already closed", ErrInvalidTransition)
		}
		return domain.PRStateClosed, nil
	case ports.SCMPRReopen:
		if !pr.Closed {
			return "", fmt.Errorf("%w: pull request is not closed", ErrInvalidTransition)
		}
		if pr.Draft {
			return domain.PRStateDraft, nil
		}
		return domain.PRStateOpen, nil
	case ports.SCMPRReady:
		if pr.Closed || !pr.Draft {
			return "", fmt.Errorf("%w: pull request is not an open draft", ErrInvalidTransition)
		}
		return domain.PRStateOpen, nil
	case ports.SCMPRDraft:
		if pr.Closed || pr.Draft {
			return "", fmt.Errorf("%w: pull request is not open and ready for review", ErrInvalidTransition)
		}
		return domain.PRStateDraft, nil
	default:
		return "", fmt.Errorf("%w: unknown state action %q", ErrInvalidAction, action)
	}
}

// PostComment posts a conversation comment, or a reply in one of the PR's
// review threads, then stores it locally and asks the observer to reconcile.
func (s *ActionService) PostComment(ctx context.Context, request CommentRequest) (CommentResult, error) {
	prNumber, err := parsePRNumber(request.PRID)
	if err != nil || strings.TrimSpace(request.PRURL) == "" {
		return CommentResult{}, fmt.Errorf("%w: invalid pull request identity", ErrInvalidPR)
	}
	if strings.TrimSpace(request.Body) == "" {
		return CommentResult{}, fmt.Errorf("%w: comment body is required", ErrInvalidComment)
	}
	if len(request.Body) > maxCommentBodyBytes {
		return CommentResult{}, fmt.Errorf("%w: comment body exceeds %d bytes", ErrInvalidComment, maxCommentBodyBytes)
	}
	if s.store == nil || s.commenter == nil {
		return CommentResult{}, errors.New("pr: comment action is not configured")
	}
	tracked, ok, err := s.store.GetPR(ctx, request.PRURL)
	if err != nil {
		return CommentResult{}, fmt.Errorf("load pull request: %w", err)
	}
	if !ok || tracked.Number != prNumber {
		return CommentResult{}, ErrPRNotFound
	}
	repo, ok := scmRepoForPR(tracked)
	if !ok {
		return CommentResult{}, fmt.Errorf("%w: pull request repository is unknown", ErrPRPreconditions)
	}
	ref := ports.SCMPRRef{Repo: repo, Number: tracked.Number, URL: tracked.URL}

	threadID := strings.TrimSpace(request.ThreadID)
	var thread *storedThread
	if threadID != "" {
		// A provider thread id is global: without this check a reply could land
		// in a thread of another PR the token can reach.
		if thread, err = s.findThread(ctx, tracked, ref, threadID); err != nil {
			return CommentResult{}, err
		}
	}

	posted, err := s.commenter.PostComment(ctx, ports.SCMCommentRequest{PR: ref, Body: request.Body, ThreadID: threadID})
	if err != nil {
		return CommentResult{}, mapMutationError("post pull request comment", err)
	}
	createdAt := posted.CreatedAt
	if createdAt.IsZero() {
		createdAt = s.clock().UTC()
	}
	s.storePostedComment(ctx, tracked, thread, posted, createdAt)
	if s.refresher != nil {
		s.refresher.RequestReviewRefresh(tracked.URL)
	}
	return CommentResult{ID: posted.ID, URL: posted.URL, CreatedAt: createdAt}, nil
}

// storedThread is a review thread confirmed to belong to the PR. Stored is
// false when only the live provider listing knew it, so there is no local row
// to attach an immediate reply to.
type storedThread struct {
	ID               string
	Path             string
	Line             int
	Resolved         bool
	Stored           bool
	AutoInjectReview bool
}

func (s *ActionService) findThread(ctx context.Context, tracked domain.PullRequest, ref ports.SCMPRRef, threadID string) (*storedThread, error) {
	if s.resolve != nil {
		threads, err := s.resolve.ListPRReviewThreads(ctx, tracked.URL)
		if err != nil {
			return nil, fmt.Errorf("load review threads: %w", err)
		}
		for _, th := range threads {
			if th.ThreadID != threadID {
				continue
			}
			found := &storedThread{ID: th.ThreadID, Path: th.Path, Line: th.Line, Resolved: th.Resolved, Stored: true}
			comments, err := s.resolve.ListPRComments(ctx, tracked.URL)
			if err != nil {
				return nil, fmt.Errorf("load review comments: %w", err)
			}
			// A reply inherits the injection decision its thread was stored with.
			for _, c := range comments {
				if c.ThreadID == threadID {
					found.AutoInjectReview = c.AutoInjectReview
				}
			}
			return found, nil
		}
	}
	if s.reader == nil {
		return nil, fmt.Errorf("%w: %q", ErrThreadNotFound, threadID)
	}
	// The thread may be newer than the last stored observation.
	review, err := s.reader.FetchReviewThreads(ctx, ref)
	if err != nil {
		if errors.Is(err, ports.ErrSCMNotFound) {
			return nil, fmt.Errorf("%w: %w", ErrPRNotFound, err)
		}
		return nil, fmt.Errorf("refresh review threads before replying: %w", err)
	}
	for _, th := range review.Threads {
		if strings.TrimSpace(th.ID) == threadID {
			return &storedThread{ID: threadID, Path: th.Path, Line: th.Line, Resolved: th.Resolved}, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrThreadNotFound, threadID)
}

func (s *ActionService) storePostedComment(ctx context.Context, tracked domain.PullRequest, thread *storedThread, posted ports.SCMCommentResult, createdAt time.Time) {
	if s.commentWriter == nil || strings.TrimSpace(posted.ID) == "" {
		return
	}
	if thread != nil {
		if !thread.Stored {
			return
		}
		err := s.commentWriter.UpsertPRReviewComment(ctx, tracked.URL, domain.PullRequestComment{
			ThreadID:         thread.ID,
			ID:               posted.ID,
			Author:           posted.Author,
			File:             thread.Path,
			Line:             thread.Line,
			Body:             posted.Body,
			URL:              posted.URL,
			Resolved:         thread.Resolved,
			IsBot:            posted.IsBot,
			CreatedAt:        createdAt,
			AutoInjectReview: thread.AutoInjectReview,
		})
		if err != nil {
			s.logger.Warn("pr comment posted but local review comment write failed", "pr", tracked.URL, "thread", thread.ID, "err", err)
		}
		return
	}
	body := posted.Body
	if len(body) > domain.MaxPullRequestCommentBodyBytes {
		body = truncateUTF8(body, domain.MaxPullRequestCommentBodyBytes)
	}
	if _, err := s.commentWriter.AppendPRConversationComment(ctx, tracked.URL, domain.PullRequestConversationComment{
		ID:        posted.ID,
		Author:    posted.Author,
		IsBot:     posted.IsBot,
		Body:      body,
		URL:       posted.URL,
		CreatedAt: createdAt,
	}); err != nil {
		s.logger.Warn("pr comment posted but local conversation write failed", "pr", tracked.URL, "err", err)
	}
}

func truncateUTF8(body string, limit int) string {
	if len(body) <= limit {
		return body
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut]
}

// mapMutationError maps provider sentinels from a PR mutation to service errors.
func mapMutationError(op string, err error) error {
	switch {
	case errors.Is(err, ports.ErrSCMNotFound):
		return fmt.Errorf("%w: %w", ErrPRNotFound, err)
	case errors.Is(err, ports.ErrSCMRejected):
		return fmt.Errorf("%w: %w", ErrPRRejected, err)
	case errors.Is(err, ports.ErrSCMUnsupported):
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	default:
		return fmt.Errorf("%s: %w", op, err)
	}
}
