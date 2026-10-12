// Package review holds the core code-review logic: triggering a reviewer over a
// worker's worktree, recording review runs, and accepting submitted results.
//
// It is independent of any transport. The daemon's HTTP service
// (internal/service/review) is a thin boundary over this engine today, and the
// same engine can back an in-process CLI trigger later without going through the
// API. Transport-specific concerns (DTOs, error→status mapping) stay in the
// service/controller layers; the orchestration and run-id generation live here.
package review

import (
	stdctx "context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrInvalid and ErrNotFound let the transport layer map failures to 422/404.
// ErrConflict marks a trigger the caller asked to reject rather than reuse; the
// two specific conflicts wrap it so the transport can name which one occurred.
var (
	ErrInvalid  = errors.New("review: invalid input")
	ErrNotFound = errors.New("review: not found")
	ErrConflict = errors.New("review: conflict")

	ErrReviewAlreadyRunning = fmt.Errorf("%w: review already running", ErrConflict)
	ErrHeadAlreadyReviewed  = fmt.Errorf("%w: head already reviewed", ErrConflict)
	// ErrPROwnedElsewhere means a named PR belongs to another active session,
	// which a review trigger never takes over.
	ErrPROwnedElsewhere = fmt.Errorf("%w: pull request owned by another session", ErrConflict)
)

// Store is the persistence surface the engine needs. *sqlite.Store satisfies it
// in production; tests use a fake.
type Store interface {
	UpsertReview(ctx stdctx.Context, r domain.Review) error
	ArchiveReviewsBySession(ctx stdctx.Context, workerID domain.SessionID, now time.Time) error
	SetReviewInterfaceMode(ctx stdctx.Context, id string, mode domain.ReviewerInterfaceMode, updatedAt time.Time) (bool, error)
	RestoreReviewLaunchState(ctx stdctx.Context, review domain.Review, now time.Time) (bool, error)
	SettleReviewChatWork(ctx stdctx.Context, reviewID string, now time.Time) error
	SetSessionReviewerConfig(ctx stdctx.Context, id domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig, updatedAt time.Time) (bool, error)
	GetReviewBySession(ctx stdctx.Context, id domain.SessionID) (domain.Review, bool, error)
	ClearReviewerHandle(ctx stdctx.Context, id domain.SessionID) error
	GetReviewBySessionAndHarness(ctx stdctx.Context, id domain.SessionID, harness domain.ReviewerHarness) (domain.Review, bool, error)
	ListReviewsBySession(ctx stdctx.Context, id domain.SessionID) ([]domain.Review, error)
	ClearReviewerHandleByHarness(ctx stdctx.Context, id domain.SessionID, harness domain.ReviewerHarness) error
	InsertReviewRun(ctx stdctx.Context, r domain.ReviewRun) error
	UpdateReviewRunResult(ctx stdctx.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body, githubReviewID string, autoInjectReview bool) (bool, error)
	UpdateReviewAgentSessionID(ctx stdctx.Context, id, agentSessionID string) (bool, error)
	SupersedeStaleRunningReviewRuns(ctx stdctx.Context, sessionID domain.SessionID, prURL, targetSHA, body string) (int64, error)
	CancelRunningReviewRunsBySession(ctx stdctx.Context, sessionID domain.SessionID, body string) (int64, error)
	CancelRunningReviewRunsBySessionAndHarness(ctx stdctx.Context, sessionID domain.SessionID, harness domain.ReviewerHarness, body string) (int64, error)
	GetReviewRun(ctx stdctx.Context, id string) (domain.ReviewRun, bool, error)
	GetReviewRunBySessionPRAndSHA(ctx stdctx.Context, id domain.SessionID, prURL, targetSHA string) (domain.ReviewRun, bool, error)
	GetReviewRunBySessionPRSHAAndHarness(ctx stdctx.Context, id domain.SessionID, prURL, targetSHA string, harness domain.ReviewerHarness) (domain.ReviewRun, bool, error)
	ListReviewRunsBySession(ctx stdctx.Context, id domain.SessionID) ([]domain.ReviewRun, error)
	ListRunningReviewRunsBySession(ctx stdctx.Context, id domain.SessionID) ([]domain.ReviewRun, error)
	ListRecoverableChatReviews(ctx stdctx.Context) ([]domain.Review, error)
	RecordReviewChatControllerError(ctx stdctx.Context, id, message string, now time.Time) (bool, error)
}

// Sessions resolves the worker session under review.
type Sessions interface {
	GetSession(ctx stdctx.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// PRs resolves the PR a worker owns.
type PRs interface {
	ListPRsBySession(ctx stdctx.Context, id domain.SessionID) ([]domain.PullRequest, error)
}

// Projects resolves the per-project reviewer config.
type Projects interface {
	GetProject(ctx stdctx.Context, id string) (domain.ProjectRecord, bool, error)
}

// Deps wires the engine.
type Deps struct {
	Store    Store
	Sessions Sessions
	PRs      PRs
	Projects Projects
	Launcher Launcher
	// ChatRecoveryDone gates operations that could mistake a recovering reviewer for an exited one.
	ChatRecoveryDone <-chan struct{}

	// Clock and NewID are injectable for deterministic tests.
	Clock func() time.Time
	NewID func() string
}

// Engine is the core code-review engine.
type Engine struct {
	store    Store
	sessions Sessions
	prs      PRs
	projects Projects
	launcher Launcher
	clock    func() time.Time
	newID    func() string

	chatRecoveryDone <-chan struct{}

	// triggerMu guards triggerLocks; triggerLocks holds one mutex per worker
	// session so concurrent Trigger calls for the same worker serialise (see
	// lockWorker). Distinct workers never contend.
	triggerMu    sync.Mutex
	triggerLocks map[domain.SessionID]*sync.Mutex
}

const autoReviewFailedRetryLimit = 3

// New wires an Engine from its dependencies, defaulting the clock and id source.
func New(d Deps) *Engine {
	clock := d.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	newID := d.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	return &Engine{
		store:        d.Store,
		sessions:     d.Sessions,
		prs:          d.PRs,
		projects:     d.Projects,
		launcher:     d.Launcher,
		clock:        clock,
		newID:        newID,
		triggerLocks: make(map[domain.SessionID]*sync.Mutex),

		chatRecoveryDone: d.ChatRecoveryDone,
	}
}

// lockWorker serialises Trigger calls for a single worker session and returns
// the unlock func. Without it, two concurrent triggers for the same worker can
// both pass the per-commit idempotency check and each spawn a reviewer against
// the same deterministic handle, leaving two running runs for one commit (#242).
//
// The per-worker mutex is created on first use and kept for the lifetime of the
// engine; the entry is a single pointer, so the unbounded-by-session-count map
// is a negligible, bounded-in-practice cost.
func (e *Engine) lockWorker(id domain.SessionID) func() {
	e.triggerMu.Lock()
	mu, ok := e.triggerLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		e.triggerLocks[id] = mu
	}
	e.triggerMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// waitForChatRecovery keeps a missing startup controller from being treated as
// proof of exit. Recovery itself bypasses this gate; provider result callbacks
// must also remain available while native conversations reconnect.
func (e *Engine) waitForChatRecovery(ctx stdctx.Context) error {
	if e.chatRecoveryDone == nil {
		return nil
	}
	select {
	case <-e.chatRecoveryDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TriggerResult is the outcome of a trigger: the (new or existing) run, the live
// reviewer pane's handle so the UI can attach its terminal, and whether a new
// pass was started (false when an existing run for the same commit was reused).
type TriggerResult struct {
	Run              domain.ReviewRun
	ReviewerHandleID string
	Created          bool
	Reviews          []PRReviewState
	Runs             []domain.ReviewRun
	CreatedRuns      []domain.ReviewRun
	ReviewerSurface  domain.ReviewerSurface
	// SkipReason is set only for a normal automatic-trigger policy race, such
	// as the worker becoming active after the coordinator's initial read.
	SkipReason string
}

// SessionReviews is a worker's review state: the live reviewer handle plus its
// recorded passes, newest first.
type SessionReviews struct {
	ReviewerHandleID      string
	ReviewerHarness       domain.ReviewerHarness
	ReviewerActivityState domain.ActivityState
	Runs                  []domain.ReviewRun
	Reviews               []PRReviewState
	ReviewerSurface       domain.ReviewerSurface
	// ActiveReviewers lists every reviewer with a live pane or running pass,
	// selected reviewer first, so clients can open each of several reviewers
	// working on the same worker at once.
	ActiveReviewers []domain.ReviewerSurface
}

// CancelResult is the review state after a reviewer pane cancellation.
type CancelResult struct {
	ReviewerHandleID string
	Reviews          []PRReviewState
	CancelledRuns    []domain.ReviewRun
}

// TerminateResult reports the reviewer pane hard-teardown performed because
// the owning worker session is leaving its live lifecycle.
type TerminateResult struct {
	ReviewerHandleID string
	CancelledRuns    []domain.ReviewRun
}

// RestoreReviewerResult reports an idle reviewer pane restored alongside its
// worker session.
type RestoreReviewerResult struct {
	ReviewerHandleID string
	Restored         bool
}

// TriggerOptions selects the reviewer, its surface, and the same-commit policy
// for one trigger.
type TriggerOptions struct {
	// Harness and Config override the resolved reviewer for this pass only.
	Harness domain.ReviewerHarness
	Config  domain.AgentConfig
	Source  domain.ReviewTriggerSource
	// InterfaceMode chooses Chat or Terminal for the reviewer; empty keeps the
	// reviewer's persisted surface or the adapter default.
	InterfaceMode domain.ReviewerInterfaceMode
	// PRURL restricts the pass to one of the worker's PRs; empty reviews every
	// eligible PR on the session.
	PRURL string
	// RejectReviewedHead turns "nothing new to review" into an error instead of
	// a silent reuse: ErrReviewAlreadyRunning when a pass is already running on
	// a PR head, ErrHeadAlreadyReviewed when every head already has a review.
	RejectReviewedHead bool
	// Rerun reviews every open PR head again even when it already has a review,
	// and lets a different reviewer run alongside one that is still running.
	// The same reviewer is never started twice on one head at the same time:
	// a person's rerun reuses that running pass, an agent's rerun is told so.
	Rerun bool
}

// TriggerWithOptions starts reviews for every PR on the worker session that needs review.
// It reuses running/up-to-date runs, retries failed/current changes-requested
// heads, and uses one reviewer pane for every new run in the batch. Automatic
// retries against the same PR head stop after three failed auto-review runs.
//
// An empty override keeps the project's configured reviewer. A known one runs
// this pass under it without editing project config, so picking a reviewer for
// one session cannot change what any other session in the project runs. The
// harness-change path below already handles the swap by respawning the pane.
func (e *Engine) TriggerWithOptions(ctx stdctx.Context, workerID domain.SessionID, opts TriggerOptions) (TriggerResult, error) {
	override, overrideConfig, source, mode := opts.Harness, opts.Config, opts.Source, opts.InterfaceMode
	if source == "" {
		source = domain.ReviewTriggerManual
	}
	if (opts.Rerun || opts.RejectReviewedHead) && source == domain.ReviewTriggerAuto {
		return TriggerResult{}, fmt.Errorf("%w: automatic review cannot request a rerun or same-commit rejection", ErrInvalid)
	}
	if workerID == "" {
		return TriggerResult{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if override != "" && !override.IsKnown() {
		return TriggerResult{}, fmt.Errorf("%w: unknown reviewer harness %q", ErrInvalid, override)
	}
	switch source {
	case domain.ReviewTriggerManual, domain.ReviewTriggerAgent, domain.ReviewTriggerAuto:
	default:
		return TriggerResult{}, fmt.Errorf("%w: unknown review trigger source %q", ErrInvalid, source)
	}
	if mode != "" && mode != domain.ReviewerInterfaceChat && mode != domain.ReviewerInterfaceTUI {
		return TriggerResult{}, fmt.Errorf("%w: unknown reviewer interface mode %q", ErrInvalid, mode)
	}
	if source == domain.ReviewTriggerAuto && mode != "" {
		return TriggerResult{}, fmt.Errorf("%w: automatic review cannot override interface mode", ErrInvalid)
	}

	// Serialise concurrent triggers for this worker so the idempotency check
	// below (and the reviewer spawn that follows it) can't be raced into a
	// double-spawn. Held across the spawn deliberately: the loser then re-reads
	// the freshly-recorded run and short-circuits to Created:false.
	if err := e.waitForChatRecovery(ctx); err != nil {
		return TriggerResult{}, err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()

	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return TriggerResult{}, err
	}
	if !ok {
		return TriggerResult{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	if source == domain.ReviewTriggerAuto {
		if reason := autoReviewSessionReason(worker, e.clock()); reason != "" {
			return TriggerResult{SkipReason: reason}, nil
		}
	}
	if worker.IsTerminated {
		return TriggerResult{}, fmt.Errorf("%w: worker session %q is terminated", ErrInvalid, workerID)
	}
	if worker.Metadata.WorkspacePath == "" {
		return TriggerResult{}, fmt.Errorf("%w: worker session %q has no workspace to review", ErrInvalid, workerID)
	}

	prs, err := e.prs.ListPRsBySession(ctx, workerID)
	if err != nil {
		return TriggerResult{}, err
	}
	if len(prs) == 0 {
		return TriggerResult{}, fmt.Errorf("%w: AO is not tracking a PR for worker %q yet; if you just opened one, run `ao review trigger --pr <url>`", ErrInvalid, workerID)
	}
	if want := strings.TrimSpace(opts.PRURL); want != "" {
		// A targeted pass reviews only the named PR, matched by API or web URL.
		var only []domain.PullRequest
		for _, pr := range prs {
			if pr.URL == want || pr.HTMLURL == want {
				only = append(only, pr)
			}
		}
		if len(only) == 0 {
			return TriggerResult{}, fmt.Errorf("%w: pull request %s is not tracked for worker %q", ErrNotFound, want, workerID)
		}
		prs = only
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return TriggerResult{}, err
	}

	if err := overrideConfig.Validate(); err != nil {
		return TriggerResult{}, fmt.Errorf("%w: reviewer config: %w", ErrInvalid, err)
	}

	harness, config, err := e.reviewerSelection(ctx, worker)
	if err != nil {
		return TriggerResult{}, err
	}
	resolvedHarness := harness
	resolvedConfig := config
	hasHarnessOverride := override != ""
	hasConfigOverride := false
	if hasHarnessOverride {
		harness = override
		if override == resolvedHarness {
			config = mergeReviewerAgentConfig(resolvedConfig, overrideConfig)
			hasConfigOverride = config != resolvedConfig
		} else if !overrideConfig.IsZero() {
			config = mergeReviewerAgentConfig(domain.AgentConfig{}, overrideConfig)
		} else {
			config = domain.AgentConfig{}
		}
	} else if !overrideConfig.IsZero() {
		config = mergeReviewerAgentConfig(config, overrideConfig)
		hasConfigOverride = config != resolvedConfig
	}
	reviewRows, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return TriggerResult{}, err
	}
	reviewRow, hasReview, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, harness)
	if err != nil {
		return TriggerResult{}, err
	}
	previousReview := reviewRow
	selectedMode := mode
	if selectedMode == "" && hasReview {
		selectedMode = reviewRow.InterfaceMode
	}
	if selectedMode == "" {
		selectedMode = e.launcher.InterfaceMode(harness)
	}
	if selectedMode == domain.ReviewerInterfaceChat && e.launcher.InterfaceMode(harness) != domain.ReviewerInterfaceChat {
		if mode != "" {
			return TriggerResult{}, fmt.Errorf("%w: reviewer %q does not support Chat", ErrInvalid, harness)
		}
		selectedMode = domain.ReviewerInterfaceTUI
	}
	// Another reviewer that is still working keeps running: a worker may ask
	// several reviewers for opinions on the same commit at once. Only idle
	// panes of other reviewers are released.
	if err := e.destroyIdleOtherReviewerHandles(ctx, workerID, harness, reviewRows, runs); err != nil {
		return TriggerResult{}, err
	}
	modeChanging := hasReview && reviewRow.InterfaceMode != "" && reviewRow.InterfaceMode != selectedMode
	secondOpinionOverride := hasConfigOverride || (modeChanging && mode != "")
	if modeChanging {
		hasConfigOverride = true
	}
	if stale, err := e.cancelStaleRunningRuns(ctx, workerID, harness, reviewRow, hasReview, runs); err != nil {
		return TriggerResult{}, err
	} else if stale {
		runs, err = e.store.ListReviewRunsBySession(ctx, workerID)
		if err != nil {
			return TriggerResult{}, err
		}
	}
	hadRunningReviewer := reviewRunsContainRunningForHarness(runs, harness)
	reviews := Plan(prs, runs)
	if source == domain.ReviewTriggerAuto || opts.Rerun {
		reviews = Plan(prs, reviewRunsForHarness(runs, harness))
		// Automatic sweeps may discover another eligible PR while this harness is
		// already reviewing one. Reconciliation above has proved the reviewer
		// terminal is still alive, so do not append work to its active batch. A
		// later sweep can start the remaining PR after the current run terminates.
		if hadRunningReviewer {
			// A person's rerun reuses the pass already running for this reviewer;
			// an agent asked for new work and must hear that none started.
			if opts.Rerun && source == domain.ReviewTriggerAgent {
				return TriggerResult{}, nothingToReviewError(reviews, runs, harness, true)
			}
			return TriggerResult{
				Run:              firstReusableRun(reviews),
				ReviewerHandleID: legacyReviewerHandle(reviewRow),
				Created:          false,
				Reviews:          reviews,
				Runs:             runs,
				ReviewerSurface:  reviewerSurface(reviewRow),
			}, nil
		}
	}

	now := e.clock()
	reviewRow, err = e.upsertReview(ctx, worker, harness, reviewRow.ReviewerHandleID, reviewRow.AgentSessionID, reviewRow.ReviewerLaunchID, domain.ActivityActive, now)
	if err != nil {
		return TriggerResult{}, err
	}

	var created []domain.ReviewRun
	type staleSupersede struct {
		prURL     string
		targetSHA string
	}
	var pendingSupersedes []staleSupersede
	var restarted []domain.ReviewRun
	batchID := ""
	for _, reviewState := range reviews {
		// A PR that is already up to date has nothing due — unless the caller asked
		// for a different reviewer than the one that produced that verdict. Picking
		// another agent is precisely a request for a second opinion on this commit,
		// so refusing it makes the reviewer choice inert exactly when it is most
		// useful. Ineligible PRs stay excluded: nothing can review those.
		eligible := reviewState.Status == ReviewStateNeedsReview || (source != domain.ReviewTriggerAuto && reviewState.Status == ReviewStateChangesRequested) || (opts.Rerun && reviewState.Status != ReviewStateIneligible)
		if source == domain.ReviewTriggerAuto && autoReviewHeadBlocked(runs, reviewState.PRURL, reviewState.TargetSHA, harness) {
			eligible = false
		}
		switch {
		case reviewState.Status == ReviewStateIneligible:
			continue
		case opts.Rerun:
			// An explicit rerun reviews the head again, except that one reviewer
			// is never started twice on the same head concurrently.
			if headRunningForHarness(runs, reviewState.PRURL, reviewState.TargetSHA, harness) {
				continue
			}
		case opts.RejectReviewedHead:
			// Any pass on this head, running or complete, by any reviewer, means
			// it is not due; the caller asked to hear about that, not reuse it.
			if headHasReview(runs, reviewState.PRURL, reviewState.TargetSHA) {
				continue
			}
		case !eligible && !secondOpinionWanted(reviewState, hasHarnessOverride, secondOpinionOverride, harness):
			continue
		}
		if hasConfigOverride {
			pendingSupersedes = append(pendingSupersedes, staleSupersede{prURL: reviewState.PRURL, targetSHA: reviewState.TargetSHA})
			if reviewState.LatestRun != nil && reviewState.LatestRun.Status == domain.ReviewRunRunning && (reviewState.LatestRun.Harness == harness || reviewState.LatestRun.Harness == "") {
				restarted = append(restarted, *reviewState.LatestRun)
				continue
			}
		} else if _, err := e.store.SupersedeStaleRunningReviewRuns(ctx, workerID, reviewState.PRURL, reviewState.TargetSHA, "superseded by a review trigger for a newer commit"); err != nil {
			return TriggerResult{}, err
		}
		if batchID == "" {
			batchID = e.newID()
		}
		run := domain.ReviewRun{
			ID:            e.newID(),
			ReviewID:      reviewRow.ID,
			SessionID:     workerID,
			BatchID:       batchID,
			Harness:       harness,
			TriggerSource: source,
			PRURL:         reviewState.PRURL,
			TargetSHA:     reviewState.TargetSHA,
			Status:        domain.ReviewRunRunning,
			Verdict:       domain.VerdictNone,
			CreatedAt:     now,
			// Completion refreshes this snapshot before delivery. Keeping the
			// trigger-time value also makes a running pass truthful in the API.
			AutoInjectReview: worker.AutoInjectReview,
		}
		if err := e.store.InsertReviewRun(ctx, run); err != nil {
			if errors.Is(err, domain.ErrDuplicateReviewRun) {
				if existing, ok, getErr := e.store.GetReviewRunBySessionPRSHAAndHarness(ctx, workerID, reviewState.PRURL, reviewState.TargetSHA, harness); getErr != nil {
					return TriggerResult{}, getErr
				} else if ok {
					reviews = replaceReviewLatestRun(reviews, reviewState.PRURL, reviewState.TargetSHA, existing)
					continue
				}
			}
			return TriggerResult{}, err
		}
		created = append(created, run)
		reviews = replaceReviewLatestRun(reviews, reviewState.PRURL, reviewState.TargetSHA, run)
	}
	if len(created) == 0 && len(restarted) == 0 {
		if modeChanging && mode == "" {
			if err := e.stopReviewChatWork(ctx, reviewRow); err != nil {
				return TriggerResult{}, err
			}
			if err := e.setReviewerInterfaceMode(ctx, reviewRow.ID, selectedMode, now); err != nil {
				return TriggerResult{}, err
			}
			if err := e.store.ClearReviewerHandleByHarness(ctx, workerID, harness); err != nil {
				return TriggerResult{}, err
			}
			if _, err := e.store.UpdateReviewAgentSessionID(ctx, reviewRow.ID, ""); err != nil {
				return TriggerResult{}, err
			}
			reviewRow.InterfaceMode = selectedMode
			reviewRow.ReviewerHandleID = ""
			reviewRow.AgentSessionID = ""
			reviewRow.ProviderConversationID = ""
			reviewRow.ControllerGeneration = ""
		}
		if opts.RejectReviewedHead || (opts.Rerun && source == domain.ReviewTriggerAgent) {
			return TriggerResult{}, nothingToReviewError(reviews, runs, harness, opts.Rerun)
		}
		return TriggerResult{Run: firstReusableRun(reviews), ReviewerHandleID: legacyReviewerHandle(reviewRow), Created: false, Reviews: reviews, Runs: runs, ReviewerSurface: reviewerSurface(reviewRow)}, nil
	}

	failRuns := func(start int, err error) error {
		for _, run := range created[start:] {
			if _, updateErr := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, err.Error(), "", run.AutoInjectReview); updateErr != nil {
				return updateErr
			}
		}
		return err
	}

	previousHandleID := reviewRow.ReviewerHandleID
	previousAgentSessionID := reviewRow.AgentSessionID
	previousLaunchID := reviewRow.ReviewerLaunchID
	deferInitialMessage := modeChanging && previousHandleID != ""
	handleID := ""
	previousStopped := false
	rollbackReplacement := func(cause error) error {
		rollbackNow := e.clock()
		if handleID != "" {
			if cleanupErr := e.launcher.Destroy(ctx, handleID); cleanupErr != nil {
				cause = errors.Join(cause, fmt.Errorf("cleanup replacement reviewer: %w", cleanupErr))
			}
		}
		if hasReview {
			if previousStopped {
				previousReview.ControllerGeneration = ""
				previousReview.ReviewerActivityState = domain.ActivityExited
			}
			rollbackCtx, cancel := stdctx.WithTimeout(stdctx.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if previousReview.IsArchived || modeChanging || (hasConfigOverride && selectedMode == domain.ReviewerInterfaceChat) {
				if ok, rollbackErr := e.store.RestoreReviewLaunchState(rollbackCtx, previousReview, rollbackNow); rollbackErr != nil {
					cause = errors.Join(cause, fmt.Errorf("restore previous reviewer surface: %w", rollbackErr))
				} else if !ok {
					cause = errors.Join(cause, fmt.Errorf("%w: reviewer %q", ErrNotFound, previousReview.ID))
				}
			} else if _, rollbackErr := e.upsertReview(rollbackCtx, worker, harness, previousHandleID, previousAgentSessionID, previousLaunchID, "", now); rollbackErr != nil {
				cause = errors.Join(cause, fmt.Errorf("rollback review row: %w", rollbackErr))
			}
		}
		if previousStopped {
			for _, run := range restarted {
				if _, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, cause.Error(), "", run.AutoInjectReview); err != nil {
					cause = errors.Join(cause, err)
				}
			}
		}
		return failRuns(0, cause)
	}

	queueRuns := append([]domain.ReviewRun{}, restarted...)
	queueRuns = append(queueRuns, created...)
	queue := reviewQueue(queueRuns)
	launchRun := queueRuns[0]
	launchAgentSessionID := reviewRow.AgentSessionID
	if hasConfigOverride || selectedMode == domain.ReviewerInterfaceChat {
		launchAgentSessionID = ""
	}
	persistedAgentSessionID := launchAgentSessionID
	if !hasConfigOverride && reviewRow.ReviewerHandleID != "" && reviewerPaneReusable(reviewRow, hadRunningReviewer) {
		alive, err := e.launcher.Alive(ctx, reviewRow.ReviewerHandleID, reviewRow.ReviewerLaunchID)
		if err != nil {
			return TriggerResult{}, failRuns(0, err)
		}
		if alive {
			handleID = reviewRow.ReviewerHandleID
		}
	}
	if handleID == "" {
		// Each pass gets a fresh reviewer process on the same stable terminal
		// handle when there is no resumable live agent session to notify.
		if err := e.launcher.Preflight(ctx, harness, worker.Metadata.WorkspacePath, selectedMode); err != nil {
			return TriggerResult{}, failRuns(0, fmt.Errorf("reviewer preflight: %w", err))
		}
		// A review owns one Chat controller. Stop it while its durable fence
		// still belongs to it before a config replacement claims a new epoch.
		if hasConfigOverride && !modeChanging && selectedMode == domain.ReviewerInterfaceChat {
			if err := e.stopReviewChatWork(ctx, reviewRow); err != nil {
				return TriggerResult{}, failRuns(0, err)
			}
			previousStopped = true
		}
		launchID := e.newID()
		reviewRow, err = e.upsertReview(ctx, worker, harness, reviewRow.ReviewerHandleID, launchAgentSessionID, launchID, "", now)
		if err != nil {
			return TriggerResult{}, rollbackReplacement(err)
		}
		if reviewRow.IsArchived {
			// Chat must be active before its controller can claim ownership. Only
			// reopen once launch work is due; rollback restores the archive on failure.
			reviewRow.IsArchived = false
			reviewRow.ReviewerActivityState = domain.ActivityActive
			if err := e.store.UpsertReview(ctx, reviewRow); err != nil {
				return TriggerResult{}, rollbackReplacement(err)
			}
		}
		if err := e.setReviewerInterfaceMode(ctx, reviewRow.ID, selectedMode, now); err != nil {
			return TriggerResult{}, rollbackReplacement(err)
		}
		projectEnv, err := e.projectEnv(ctx, worker.ProjectID)
		if err != nil {
			return TriggerResult{}, rollbackReplacement(err)
		}
		launchSpec := reviewLaunchSpec(worker, harness, config, launchRun, queue, 0, launchAgentSessionID, launchID)
		launchSpec.ProjectEnv = projectEnv
		launchSpec.InterfaceMode = selectedMode
		launchSpec.DeferInitialMessage = deferInitialMessage
		if selectedMode == domain.ReviewerInterfaceChat && !hasConfigOverride {
			launchSpec.ProviderConversationID = reviewRow.ProviderConversationID
		}
		launch, err := e.launcher.Spawn(ctx, launchSpec)
		if err != nil {
			return TriggerResult{}, rollbackReplacement(fmt.Errorf("launch reviewer: %w", err))
		}
		handleID = launch.HandleID
		if launch.LaunchID != "" {
			reviewRow.ReviewerLaunchID = launch.LaunchID
		}
		if launch.AgentSessionID != "" {
			persistedAgentSessionID = launch.AgentSessionID
		}
	} else {
		launchSpec := reviewLaunchSpec(worker, harness, config, launchRun, queue, 0, reviewRow.AgentSessionID, reviewRow.ReviewerLaunchID)
		launchSpec.InterfaceMode = selectedMode
		if err := e.launcher.Notify(ctx, handleID, launchSpec); err != nil {
			return TriggerResult{}, failRuns(0, fmt.Errorf("notify reviewer: %w", err))
		}
	}
	for _, stale := range pendingSupersedes {
		if _, err := e.store.SupersedeStaleRunningReviewRuns(ctx, workerID, stale.prURL, stale.targetSHA, "superseded by a review trigger for a newer commit"); err != nil {
			if previousReview.IsArchived || modeChanging {
				return TriggerResult{}, rollbackReplacement(err)
			}
			if handleID != "" {
				_ = e.launcher.Destroy(ctx, handleID)
			}
			return TriggerResult{}, failRuns(0, err)
		}
	}
	if hasConfigOverride && persistedAgentSessionID == "" && reviewRow.ID != "" {
		if _, err := e.store.UpdateReviewAgentSessionID(ctx, reviewRow.ID, ""); err != nil {
			if previousReview.IsArchived || modeChanging {
				return TriggerResult{}, rollbackReplacement(err)
			}
			if handleID != "" {
				_ = e.launcher.Destroy(ctx, handleID)
			}
			return TriggerResult{}, failRuns(0, err)
		}
	}
	reviewRow, err = e.upsertReview(ctx, worker, harness, handleID, persistedAgentSessionID, reviewRow.ReviewerLaunchID, "", now)
	if err != nil {
		if previousReview.IsArchived || modeChanging {
			return TriggerResult{}, rollbackReplacement(err)
		}
		if handleID != "" {
			_ = e.launcher.Destroy(ctx, handleID)
		}
		return TriggerResult{}, err
	}
	if hasConfigOverride && previousHandleID != "" && previousHandleID != handleID {
		if err := e.launcher.Destroy(ctx, previousHandleID); err != nil {
			if modeChanging {
				return TriggerResult{}, rollbackReplacement(fmt.Errorf("destroy previous reviewer: %w", err))
			}
			if _, rollbackErr := e.upsertReview(ctx, worker, harness, previousHandleID, previousAgentSessionID, "", "", now); rollbackErr != nil {
				return TriggerResult{}, failRuns(0, fmt.Errorf("destroy previous reviewer: %w; rollback review row: %w", err, rollbackErr))
			}
			if handleID != "" {
				if destroyNewErr := e.launcher.Destroy(ctx, handleID); destroyNewErr != nil {
					return TriggerResult{}, failRuns(0, fmt.Errorf("destroy previous reviewer: %w; cleanup replacement reviewer: %w", err, destroyNewErr))
				}
			}
			return TriggerResult{}, failRuns(0, fmt.Errorf("destroy previous reviewer: %w", err))
		}
		previousStopped = true
		if deferInitialMessage {
			launchSpec := reviewLaunchSpec(worker, harness, config, launchRun, queue, 0, persistedAgentSessionID, reviewRow.ReviewerLaunchID)
			launchSpec.InterfaceMode = selectedMode
			if err := e.launcher.Notify(ctx, handleID, launchSpec); err != nil {
				return TriggerResult{}, rollbackReplacement(fmt.Errorf("notify replacement reviewer: %w", err))
			}
		}
	}
	for i := range created {
		created[i].ReviewID = reviewRow.ID
	}
	triggerRuns := append([]domain.ReviewRun{}, created...)
	triggerRuns = append(triggerRuns, runs...)
	resultRun := launchRun
	createdFlag := len(created) > 0 || len(restarted) > 0
	return TriggerResult{Run: resultRun, ReviewerHandleID: legacyReviewerHandle(reviewRow), Created: createdFlag, Reviews: reviews, Runs: triggerRuns, CreatedRuns: created, ReviewerSurface: reviewerSurface(reviewRow)}, nil
}

func autoReviewSessionReason(worker domain.SessionRecord, now time.Time) string {
	switch {
	case !worker.AutoReviewEnabled:
		return "disabled"
	case worker.Kind != domain.KindWorker:
		return "not_worker"
	case worker.IsTerminated:
		return "terminated"
	case worker.Activity.State != domain.ActivityIdle:
		return "not_idle"
	case worker.Activity.LastActivityAt.IsZero() || now.Sub(worker.Activity.LastActivityAt) < time.Minute:
		return "idle_threshold_not_met"
	default:
		return ""
	}
}

// headHasReview reports whether any reviewer has a running or completed pass
// on the PR head. Failed and cancelled passes never produced a review.
func headHasReview(runs []domain.ReviewRun, prURL, targetSHA string) bool {
	for _, run := range runs {
		if run.PRURL != prURL || run.TargetSHA != targetSHA {
			continue
		}
		if run.Status == domain.ReviewRunRunning || run.Verdict == domain.VerdictApproved || run.Verdict == domain.VerdictChangesRequested {
			return true
		}
	}
	return false
}

func headRunningForHarness(runs []domain.ReviewRun, prURL, targetSHA string, harness domain.ReviewerHarness) bool {
	for _, run := range runs {
		if run.PRURL == prURL && run.TargetSHA == targetSHA && run.Status == domain.ReviewRunRunning && (run.Harness == harness || run.Harness == "") {
			return true
		}
	}
	return false
}

// nothingToReviewError explains why a trigger that asked not to reuse silently
// started nothing. It names the first reviewable PR head, so the caller sees
// which commit is already covered and what to do next.
func nothingToReviewError(reviews []PRReviewState, runs []domain.ReviewRun, harness domain.ReviewerHarness, rerun bool) error {
	for _, review := range reviews {
		if review.Status == ReviewStateIneligible {
			continue
		}
		sha := shortSHA(review.TargetSHA)
		if rerun {
			return fmt.Errorf("%w: %s is already reviewing PR #%d head %s; wait for it, cancel it with `ao review cancel`, or choose another reviewer with --agent", ErrReviewAlreadyRunning, harness, review.PRNumber, sha)
		}
		for _, run := range runs {
			if run.PRURL == review.PRURL && run.TargetSHA == review.TargetSHA && run.Status == domain.ReviewRunRunning {
				return fmt.Errorf("%w: %s is already reviewing PR #%d head %s; wait for its result, or pass --rerun to add another reviewer", ErrReviewAlreadyRunning, run.Harness, review.PRNumber, sha)
			}
		}
		verdict := "reviewed"
		if review.LatestRun != nil && review.LatestRun.Verdict != domain.VerdictNone {
			verdict = string(review.LatestRun.Verdict)
		}
		return fmt.Errorf("%w: PR #%d head %s was already reviewed (%s); push new commits, or pass --rerun to review this commit again. If you just pushed, wait a few seconds and run it again", ErrHeadAlreadyReviewed, review.PRNumber, sha, verdict)
	}
	return fmt.Errorf("%w: no open PR head to review", ErrInvalid)
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// SwitchReviewer serializes reviewer preference changes with trigger/restore
// and returns the authoritative post-switch review state.
func (e *Engine) SwitchReviewer(
	ctx stdctx.Context,
	workerID domain.SessionID,
	harness domain.ReviewerHarness,
	config domain.AgentConfig,
) (SessionReviews, error) {
	if workerID == "" {
		return SessionReviews{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if harness != "" && !harness.IsKnown() {
		return SessionReviews{}, fmt.Errorf("%w: unknown reviewer harness %q", ErrInvalid, harness)
	}
	if err := config.Validate(); err != nil {
		return SessionReviews{}, fmt.Errorf("%w: reviewer config: %w", ErrInvalid, err)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return SessionReviews{}, err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()

	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	if !ok {
		return SessionReviews{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	previousSelected, previousConfig, err := e.reviewerSelection(ctx, worker)
	if err != nil {
		return SessionReviews{}, err
	}
	if ok, err := e.store.SetSessionReviewerConfig(ctx, workerID, harness, config, e.clock()); err != nil {
		return SessionReviews{}, err
	} else if !ok {
		return SessionReviews{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	worker.ReviewerHarness = harness
	worker.ReviewerConfig = config
	selected, selectedConfig, err := e.reviewerSelection(ctx, worker)
	if err != nil {
		return SessionReviews{}, err
	}
	reviewRows, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	if err := e.destroyOtherReviewerHandles(ctx, workerID, selected, reviewRows); err != nil {
		return SessionReviews{}, err
	}
	if previousSelected == selected && previousConfig != selectedConfig {
		if err := e.resetReviewerRuntimeLocked(ctx, workerID, selected); err != nil {
			return SessionReviews{}, err
		}
	}
	if _, err := e.restoreReviewerLocked(ctx, workerID, worker, selected, selectedConfig); err != nil {
		return SessionReviews{}, err
	}
	return e.listLocked(ctx, workerID, selected)
}

func reviewerPaneReusable(reviewRow domain.Review, hadRunningReviewer bool) bool {
	if strings.TrimSpace(reviewRow.AgentSessionID) != "" {
		return true
	}
	return hadRunningReviewer
}

func reviewRunsContainRunningForHarness(runs []domain.ReviewRun, harness domain.ReviewerHarness) bool {
	for _, run := range runs {
		if (run.Harness == harness || run.Harness == "") && run.Status == domain.ReviewRunRunning {
			return true
		}
	}
	return false
}

func (e *Engine) destroyOtherReviewerHandles(ctx stdctx.Context, workerID domain.SessionID, selected domain.ReviewerHarness, reviews []domain.Review) error {
	for _, review := range reviews {
		if review.Harness == selected || review.ReviewerHandleID == "" {
			continue
		}
		if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
			return err
		}
		if err := e.store.ClearReviewerHandleByHarness(ctx, workerID, review.Harness); err != nil {
			return err
		}
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, workerID, review.Harness, "cancelled because reviewer agent was switched"); err != nil {
			return err
		}
	}
	return nil
}

// destroyIdleOtherReviewerHandles releases other reviewers' panes that have no
// running pass, while leaving any reviewer that is still working alone.
func (e *Engine) destroyIdleOtherReviewerHandles(ctx stdctx.Context, workerID domain.SessionID, selected domain.ReviewerHarness, reviews []domain.Review, runs []domain.ReviewRun) error {
	idle := make([]domain.Review, 0, len(reviews))
	for _, review := range reviews {
		if review.Harness != selected && !reviewRunsContainRunningForReview(runs, review) {
			idle = append(idle, review)
		}
	}
	return e.destroyOtherReviewerHandles(ctx, workerID, selected, idle)
}

func reviewRunsContainRunningForReview(runs []domain.ReviewRun, review domain.Review) bool {
	for _, run := range runs {
		if run.Status == domain.ReviewRunRunning && (run.ReviewID == review.ID || run.Harness == review.Harness) {
			return true
		}
	}
	return false
}

func (e *Engine) resetReviewerRuntimeLocked(ctx stdctx.Context, workerID domain.SessionID, harness domain.ReviewerHarness) error {
	review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, harness)
	if err != nil || !ok {
		return err
	}
	if review.ReviewerHandleID != "" {
		if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
			return err
		}
		if err := e.store.ClearReviewerHandleByHarness(ctx, workerID, harness); err != nil {
			return err
		}
	}
	if _, err := e.store.UpdateReviewAgentSessionID(ctx, review.ID, ""); err != nil {
		return err
	}
	return nil
}

// RestoreReviewer relaunches the reviewer terminal for a restored worker when
// that worker already has review history. It does not create review_run rows or
// start a review; explicit trigger remains the only path that starts review
// work.
func (e *Engine) RestoreReviewer(ctx stdctx.Context, workerID domain.SessionID) (RestoreReviewerResult, error) {
	if workerID == "" {
		return RestoreReviewerResult{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return RestoreReviewerResult{}, err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()
	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	if !ok {
		return RestoreReviewerResult{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	if worker.IsTerminated || worker.Metadata.WorkspacePath == "" {
		return RestoreReviewerResult{}, nil
	}
	harness, config, err := e.reviewerSelection(ctx, worker)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	return e.restoreReviewerLocked(ctx, workerID, worker, harness, config)
}

// RecoverChatReviewers restores each durable chat reviewer by its persisted
// review row. It must not re-resolve the worker's current reviewer preference:
// that preference may have changed since this particular reviewer started.
func (e *Engine) RecoverChatReviewers(ctx stdctx.Context) error {
	reviews, err := e.store.ListRecoverableChatReviews(ctx)
	if err != nil {
		return err
	}
	var recoveryErrors []error
	for _, review := range reviews {
		if _, restoreErr := e.restoreRecoverableChatReviewer(ctx, review); restoreErr != nil {
			if _, recordErr := e.store.RecordReviewChatControllerError(ctx, review.ID, restoreErr.Error(), e.clock()); recordErr != nil {
				recoveryErrors = append(recoveryErrors, fmt.Errorf("record reviewer %s recovery error: %w", review.ID, recordErr))
			}
			recoveryErrors = append(recoveryErrors, fmt.Errorf("recover reviewer %s: %w", review.ID, restoreErr))
		}
	}
	return errors.Join(recoveryErrors...)
}

func (e *Engine) restoreRecoverableChatReviewer(ctx stdctx.Context, review domain.Review) (RestoreReviewerResult, error) {
	unlock := e.lockWorker(review.SessionID)
	defer unlock()
	current, exists, err := e.store.GetReviewBySessionAndHarness(ctx, review.SessionID, review.Harness)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	if !exists || current.ID != review.ID || current.IsArchived {
		return RestoreReviewerResult{}, nil
	}
	review = current
	worker, ok, err := e.sessions.GetSession(ctx, review.SessionID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	if !ok {
		return RestoreReviewerResult{}, fmt.Errorf("%w: worker session %q", ErrNotFound, review.SessionID)
	}
	if worker.IsTerminated || worker.Metadata.WorkspacePath == "" {
		return RestoreReviewerResult{}, nil
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, review.SessionID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	return e.restorePersistedChatReviewerLocked(ctx, worker, review, reviewRunsForReview(runs, review.ID))
}

func (e *Engine) restorePersistedChatReviewerLocked(ctx stdctx.Context, worker domain.SessionRecord, review domain.Review, previousRuns []domain.ReviewRun) (RestoreReviewerResult, error) {
	if review.ReviewerHandleID != "" {
		alive, err := e.launcher.Alive(ctx, review.ReviewerHandleID, review.ReviewerLaunchID)
		if err != nil {
			return RestoreReviewerResult{}, err
		}
		if alive {
			return RestoreReviewerResult{ReviewerHandleID: review.ReviewerHandleID}, nil
		}
	}
	if review.InterfaceMode == domain.ReviewerInterfaceChat && e.launcher.InterfaceMode(review.Harness) != domain.ReviewerInterfaceChat {
		if err := e.stopReviewChatWork(ctx, review); err != nil {
			return RestoreReviewerResult{}, err
		}
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, worker.ID, review.Harness, "cancelled because reviewer Chat is unavailable"); err != nil {
			return RestoreReviewerResult{}, err
		}
		if err := e.setReviewerInterfaceMode(ctx, review.ID, domain.ReviewerInterfaceTUI, e.clock()); err != nil {
			return RestoreReviewerResult{}, err
		}
		if _, err := e.store.UpdateReviewAgentSessionID(ctx, review.ID, ""); err != nil {
			return RestoreReviewerResult{}, err
		}
		review.InterfaceMode = domain.ReviewerInterfaceTUI
		review.AgentSessionID = ""
		review.ProviderConversationID = ""
	}
	launchID := e.newID()
	projectEnv, err := e.projectEnv(ctx, worker.ProjectID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	launch, err := e.launcher.RestoreTerminal(ctx, LaunchSpec{ReviewSessionID: review.ID, LaunchID: launchID, WorkerID: worker.ID, ProjectID: worker.ProjectID, ProjectEnv: projectEnv, Harness: review.Harness, WorkspacePath: worker.Metadata.WorkspacePath, AgentSessionID: review.AgentSessionID, ProviderConversationID: review.ProviderConversationID, PreviousRuns: previousRuns, InterfaceMode: review.InterfaceMode})
	if err != nil {
		return RestoreReviewerResult{}, fmt.Errorf("restore reviewer: %w", err)
	}
	agentSessionID := review.AgentSessionID
	if launch.AgentSessionID != "" {
		agentSessionID = launch.AgentSessionID
	}
	if _, err := e.upsertReview(ctx, worker, review.Harness, launch.HandleID, agentSessionID, launch.LaunchID, "", e.clock()); err != nil {
		_ = e.launcher.Destroy(ctx, launch.HandleID)
		return RestoreReviewerResult{}, err
	}
	return RestoreReviewerResult{ReviewerHandleID: launch.HandleID, Restored: true}, nil
}

func (e *Engine) restoreReviewerLocked(
	ctx stdctx.Context,
	workerID domain.SessionID,
	worker domain.SessionRecord,
	harness domain.ReviewerHarness,
	config domain.AgentConfig,
) (RestoreReviewerResult, error) {
	reviewRows, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	// Restoring the selected reviewer must not cancel another reviewer that is
	// still running alongside it; only idle panes of other reviewers go. One
	// whose pane did not survive cannot finish, so its passes are cancelled
	// rather than left looking like a review still in progress.
	if err := e.destroyIdleOtherReviewerHandles(ctx, workerID, harness, reviewRows, runs); err != nil {
		return RestoreReviewerResult{}, err
	}
	for _, other := range reviewRows {
		if other.Harness == harness || !reviewRunsContainRunningForReview(runs, other) {
			continue
		}
		// A Chat reviewer has no pane by design; RecoverChatReviewers restores
		// its durable conversation, so a missing handle is not a dead reviewer.
		if other.InterfaceMode == domain.ReviewerInterfaceChat {
			continue
		}
		if _, err := e.cancelStaleRunningRuns(ctx, workerID, other.Harness, other, true, runs); err != nil {
			return RestoreReviewerResult{}, err
		}
	}
	reviewRow, hasReview, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, harness)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	if hasReview && reviewRow.IsArchived {
		return RestoreReviewerResult{}, nil
	}
	previousRuns := reviewRunsForHarness(runs, harness)
	if !hasReview && len(previousRuns) == 0 {
		return RestoreReviewerResult{}, nil
	}
	if hasReview && reviewRow.ReviewerHandleID != "" {
		alive, err := e.launcher.Alive(ctx, reviewRow.ReviewerHandleID, reviewRow.ReviewerLaunchID)
		if err != nil {
			return RestoreReviewerResult{}, err
		}
		if alive {
			return RestoreReviewerResult{ReviewerHandleID: reviewRow.ReviewerHandleID, Restored: false}, nil
		}
	}
	agentSessionID := ""
	if hasReview {
		agentSessionID = reviewRow.AgentSessionID
	} else {
		reviewRow, err = e.upsertReview(ctx, worker, harness, "", "", "", domain.ActivityIdle, e.clock())
		if err != nil {
			return RestoreReviewerResult{}, err
		}
	}
	launchID := e.newID()
	// A reviewer being relaunched is no longer "exited". Clear that before the
	// launch so clients stop offering Restore for a reviewer that is back, while
	// state the new process reports through its hooks during launch still wins
	// (the finalizing upsert below preserves it).
	restoredActivity := domain.ActivityState("")
	wasExited := reviewRow.ReviewerActivityState == domain.ActivityExited
	if wasExited {
		restoredActivity = domain.ActivityIdle
	}
	reviewRow, err = e.upsertReview(ctx, worker, harness, reviewRow.ReviewerHandleID, agentSessionID, launchID, restoredActivity, e.clock())
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	if !hasReview {
		reviewRow.InterfaceMode = e.launcher.InterfaceMode(harness)
		if err := e.setReviewerInterfaceMode(ctx, reviewRow.ID, reviewRow.InterfaceMode, e.clock()); err != nil {
			return RestoreReviewerResult{}, err
		}
	} else if reviewRow.InterfaceMode == domain.ReviewerInterfaceChat && e.launcher.InterfaceMode(harness) != domain.ReviewerInterfaceChat {
		if err := e.stopReviewChatWork(ctx, reviewRow); err != nil {
			return RestoreReviewerResult{}, err
		}
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, worker.ID, harness, "cancelled because reviewer Chat is unavailable"); err != nil {
			return RestoreReviewerResult{}, err
		}
		if err := e.setReviewerInterfaceMode(ctx, reviewRow.ID, domain.ReviewerInterfaceTUI, e.clock()); err != nil {
			return RestoreReviewerResult{}, err
		}
		if _, err := e.store.UpdateReviewAgentSessionID(ctx, reviewRow.ID, ""); err != nil {
			return RestoreReviewerResult{}, err
		}
		reviewRow.InterfaceMode = domain.ReviewerInterfaceTUI
		reviewRow.AgentSessionID = ""
		agentSessionID = ""
	}
	projectEnv, err := e.projectEnv(ctx, worker.ProjectID)
	if err != nil {
		return RestoreReviewerResult{}, err
	}
	launch, err := e.launcher.RestoreTerminal(ctx, LaunchSpec{
		ReviewSessionID:      reviewRow.ID,
		LaunchID:             launchID,
		WorkerID:             worker.ID,
		ProjectID:            worker.ProjectID,
		ProjectEnv:           projectEnv,
		Harness:              harness,
		AgentConfig:          config,
		WorkspacePath:        worker.Metadata.WorkspacePath,
		AgentSessionID:       agentSessionID,
		RequireNativeHistory: harness == domain.ReviewerCodex && strings.TrimSpace(agentSessionID) != "",
		PreviousRuns:         previousRuns,
		InterfaceMode:        reviewRow.InterfaceMode,
	})
	if err != nil {
		restoreErr := fmt.Errorf("restore reviewer: %w", err)
		if wasExited {
			// The relaunch failed, so the reviewer is still gone.
			if _, markErr := e.upsertReview(ctx, worker, harness, reviewRow.ReviewerHandleID, agentSessionID, launchID, domain.ActivityExited, e.clock()); markErr != nil {
				restoreErr = errors.Join(restoreErr, markErr)
			}
		}
		if failErr := e.failRunningRestoredRuns(ctx, previousRuns, restoreErr.Error()); failErr != nil {
			return RestoreReviewerResult{}, errors.Join(restoreErr, failErr)
		}
		return RestoreReviewerResult{}, restoreErr
	}
	if !launch.NativeResumed {
		const reason = "reviewer native conversation was unavailable during restore; retry the review"
		if err := e.failRunningRestoredRuns(ctx, previousRuns, reason); err != nil {
			_ = e.launcher.Destroy(ctx, launch.HandleID)
			return RestoreReviewerResult{}, err
		}
	}
	if launch.AgentSessionID != "" {
		agentSessionID = launch.AgentSessionID
	}
	if _, err := e.upsertReview(ctx, worker, harness, launch.HandleID, agentSessionID, launch.LaunchID, "", e.clock()); err != nil {
		_ = e.launcher.Destroy(ctx, launch.HandleID)
		return RestoreReviewerResult{}, err
	}
	return RestoreReviewerResult{ReviewerHandleID: launch.HandleID, Restored: true}, nil
}

func (e *Engine) failRunningRestoredRuns(ctx stdctx.Context, runs []domain.ReviewRun, body string) error {
	for _, run := range runs {
		if run.Status != domain.ReviewRunRunning {
			continue
		}
		if _, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, body, "", run.AutoInjectReview); err != nil {
			return fmt.Errorf("fail review run %q after reviewer restore: %w", run.ID, err)
		}
	}
	return nil
}

// TeardownReviewerTerminal destroys reviewer panes before shutdown removes the
// worker worktree, but preserves review rows and native agent session ids so
// worker restore can recreate the reviewer terminal later.
func (e *Engine) TeardownReviewerTerminal(ctx stdctx.Context, workerID domain.SessionID) error {
	if workerID == "" {
		return fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()
	reviews, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return err
	}
	for _, review := range reviews {
		if review.ReviewerHandleID == "" {
			continue
		}
		if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
			return err
		}
	}
	if len(reviews) > 0 {
		if err := e.store.ClearReviewerHandle(ctx, workerID); err != nil {
			return err
		}
	}
	return nil
}

func autoReviewHeadBlocked(runs []domain.ReviewRun, prURL, targetSHA string, harness domain.ReviewerHarness) bool {
	failedAutoRuns := 0
	for _, run := range runs {
		if run.PRURL != prURL || run.TargetSHA != targetSHA || (run.Harness != harness && run.Harness != "") {
			continue
		}
		if run.Status == domain.ReviewRunRunning || run.Status == domain.ReviewRunCancelled || run.Verdict == domain.VerdictApproved || run.Verdict == domain.VerdictChangesRequested {
			return true
		}
		if run.Status == domain.ReviewRunFailed && run.TriggerSource == domain.ReviewTriggerAuto {
			failedAutoRuns++
			if failedAutoRuns >= autoReviewFailedRetryLimit {
				return true
			}
		}
	}
	return false
}

func reviewRunsForHarness(runs []domain.ReviewRun, harness domain.ReviewerHarness) []domain.ReviewRun {
	out := make([]domain.ReviewRun, 0, len(runs))
	for _, run := range runs {
		if run.Harness == harness {
			out = append(out, run)
		}
	}
	return out
}

func reviewRunsForReview(runs []domain.ReviewRun, reviewID string) []domain.ReviewRun {
	out := make([]domain.ReviewRun, 0, len(runs))
	for _, run := range runs {
		if run.ReviewID == reviewID {
			out = append(out, run)
		}
	}
	return out
}

// cancelStaleRunningRuns fails this reviewer's running passes when its pane is
// gone. It is scoped to one reviewer so a dead pane cannot cancel another
// reviewer that is still working on the same worker.
func (e *Engine) cancelStaleRunningRuns(ctx stdctx.Context, workerID domain.SessionID, harness domain.ReviewerHarness, reviewRow domain.Review, hasReview bool, runs []domain.ReviewRun) (bool, error) {
	hasRunning := false
	for _, run := range runs {
		if run.SessionID == workerID && run.Status == domain.ReviewRunRunning && run.Verdict == domain.VerdictNone && (run.Harness == harness || run.Harness == "") {
			hasRunning = true
			break
		}
	}
	if !hasRunning {
		return false, nil
	}
	if hasReview && reviewRow.ReviewerHandleID != "" {
		alive, err := e.launcher.Alive(ctx, reviewRow.ReviewerHandleID, reviewRow.ReviewerLaunchID)
		if err != nil {
			return false, err
		}
		if alive {
			return false, nil
		}
	}
	for _, h := range []domain.ReviewerHarness{harness, ""} {
		// Legacy rows predate per-harness runs and belong to whichever reviewer
		// is current, so they go with it.
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, workerID, h, "cancelled because reviewer terminal is unavailable"); err != nil {
			return false, err
		}
	}
	return true, nil
}

func reviewLaunchSpec(
	worker domain.SessionRecord,
	harness domain.ReviewerHarness,
	config domain.AgentConfig,
	run domain.ReviewRun,
	queue []ports.ReviewTask,
	index int,
	agentSessionID string,
	launchID string,
) LaunchSpec {
	return LaunchSpec{
		RunID:           run.ID,
		BatchID:         run.BatchID,
		ReviewSessionID: run.ReviewID,
		LaunchID:        launchID,
		WorkerID:        worker.ID,
		ProjectID:       worker.ProjectID,
		Harness:         harness,
		AgentConfig:     config,
		WorkspacePath:   worker.Metadata.WorkspacePath,
		AgentSessionID:  agentSessionID,
		PreviousRuns:    nil,
		PRURL:           run.PRURL,
		TargetSHA:       run.TargetSHA,
		ReviewQueue:     queue,
		ReviewIndex:     index,
	}
}

func reviewQueue(runs []domain.ReviewRun) []ports.ReviewTask {
	queue := make([]ports.ReviewTask, 0, len(runs))
	for _, run := range runs {
		queue = append(queue, ports.ReviewTask{
			RunID:     run.ID,
			PRURL:     run.PRURL,
			TargetSHA: run.TargetSHA,
		})
	}
	return queue
}

// secondOpinionWanted reports whether an explicit manual override makes an
// otherwise up-to-date PR worth running again. A config-only override (for
// example, picking a model under the same harness) always requests another
// pass. A harness-only override only does so when it actually changes the
// reviewer whose pass is currently authoritative; re-picking the same harness
// must still reuse. That remains true even while another harness is already
// running: a different reviewer is a second opinion, not a restart. Legacy
// runs may have an empty harness, which means "the same resolved harness as
// today" for this comparison.
func secondOpinionWanted(state PRReviewState, hasHarnessOverride, hasConfigOverride bool, harness domain.ReviewerHarness) bool {
	if state.Status == ReviewStateIneligible {
		return false
	}
	if state.LatestRun == nil {
		return false
	}
	if hasConfigOverride {
		return true
	}
	if !hasHarnessOverride {
		return false
	}
	latestHarness := state.LatestRun.Harness
	if latestHarness == "" {
		latestHarness = harness
	}
	return latestHarness != harness
}

func replaceReviewLatestRun(reviews []PRReviewState, prURL, targetSHA string, run domain.ReviewRun) []PRReviewState {
	for i := range reviews {
		if reviews[i].PRURL == prURL && reviews[i].TargetSHA == targetSHA {
			reviews[i].LatestRun = &run
			if run.Status == domain.ReviewRunRunning {
				reviews[i].Status = ReviewStateRunning
			}
			break
		}
	}
	return reviews
}

func firstReusableRun(reviews []PRReviewState) domain.ReviewRun {
	// Legacy compatibility only: in the multi-PR model the authoritative state
	// is Reviews. When no run is created, this field is just a best-effort
	// non-empty run for older clients.
	for _, review := range reviews {
		if review.LatestRun != nil {
			return *review.LatestRun
		}
	}
	return domain.ReviewRun{}
}

// List returns a worker's review state: the live reviewer handle and its passes.
func (e *Engine) List(ctx stdctx.Context, workerID domain.SessionID) (SessionReviews, error) {
	if workerID == "" {
		return SessionReviews{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return SessionReviews{}, err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()

	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	if !ok {
		return SessionReviews{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	selectedHarness, _, err := e.reviewerSelection(ctx, worker)
	if err != nil {
		return SessionReviews{}, err
	}
	return e.listLocked(ctx, workerID, selectedHarness)
}

func (e *Engine) listLocked(ctx stdctx.Context, workerID domain.SessionID, selectedHarness domain.ReviewerHarness) (SessionReviews, error) {
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	var reviewRow domain.Review
	reviewerHarness := selectedHarness
	if review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, selectedHarness); err != nil {
		return SessionReviews{}, err
	} else if ok {
		reviewRow = review
		reviewerHarness = review.Harness
	} else if review, ok, err := e.store.GetReviewBySession(ctx, workerID); err != nil {
		return SessionReviews{}, err
	} else if ok {
		reviewRow = review
		reviewerHarness = review.Harness
	}
	if changed, err := e.reconcileExitedReviewer(ctx, &reviewRow, runs); err != nil {
		return SessionReviews{}, err
	} else if changed {
		runs, err = e.store.ListReviewRunsBySession(ctx, workerID)
		if err != nil {
			return SessionReviews{}, err
		}
	}
	reviewRows, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	active := []domain.ReviewerSurface{}
	if reviewerRowActive(reviewRow, runs) {
		active = append(active, reviewerSurface(reviewRow))
	}
	for i := range reviewRows {
		other := reviewRows[i]
		if other.ID == reviewRow.ID {
			continue
		}
		// Other reviewers can be running alongside the selected one; a pane that
		// died mid-pass must fail its runs the same way the selected one does.
		if changed, err := e.reconcileExitedReviewer(ctx, &other, runs); err != nil {
			return SessionReviews{}, err
		} else if changed {
			runs, err = e.store.ListReviewRunsBySession(ctx, workerID)
			if err != nil {
				return SessionReviews{}, err
			}
		}
		if reviewerRowActive(other, runs) {
			active = append(active, reviewerSurface(other))
		}
	}
	prs, err := e.prs.ListPRsBySession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	return SessionReviews{
		ReviewerHandleID:      legacyReviewerHandle(reviewRow),
		ReviewerHarness:       reviewerHarness,
		ReviewerActivityState: reviewRow.ReviewerActivityState,
		Runs:                  runs,
		Reviews:               Plan(prs, runs),
		ReviewerSurface:       reviewerSurface(reviewRow),
		ActiveReviewers:       active,
	}, nil
}

// reviewerRowActive reports whether a reviewer has a pane to open or a pass
// still running. An archived reviewer is retired and never listed as active.
func reviewerRowActive(review domain.Review, runs []domain.ReviewRun) bool {
	if review.ID == "" || review.IsArchived {
		return false
	}
	if review.ReviewerHandleID != "" && review.ReviewerActivityState != domain.ActivityExited {
		return true
	}
	for _, run := range runs {
		if run.ReviewID == review.ID && run.Status == domain.ReviewRunRunning {
			return true
		}
	}
	return false
}

const reviewerExitedBeforeSubmission = "reviewer process exited before submitting a result"

// reconcileExitedReviewer converts a definitively dead active reviewer into
// durable failed-run facts. Probe errors are intentionally ignored: an unknown
// runtime state is not proof that the reviewer exited.
func (e *Engine) reconcileExitedReviewer(ctx stdctx.Context, review *domain.Review, runs []domain.ReviewRun) (bool, error) {
	if review.ID == "" || review.ReviewerHandleID == "" || review.ReviewerActivityState != domain.ActivityActive {
		return false, nil
	}
	hasRunning := false
	for _, run := range runs {
		if run.ReviewID == review.ID && run.Status == domain.ReviewRunRunning {
			hasRunning = true
			break
		}
	}
	if !hasRunning {
		return false, nil
	}
	alive, err := e.launcher.Alive(ctx, review.ReviewerHandleID, review.ReviewerLaunchID)
	if err != nil {
		// An unavailable probe is unknown, not evidence that the reviewer died.
		alive = true
	}
	if alive {
		return false, nil
	}
	for _, run := range runs {
		if run.ReviewID != review.ID || run.Status != domain.ReviewRunRunning {
			continue
		}
		if _, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, reviewerExitedBeforeSubmission, "", run.AutoInjectReview); err != nil {
			return false, err
		}
	}
	review.ReviewerActivityState = domain.ActivityExited
	review.UpdatedAt = e.clock()
	if err := e.store.UpsertReview(ctx, *review); err != nil {
		return false, err
	}
	return true, nil
}

func reviewerSurface(review domain.Review) domain.ReviewerSurface {
	if review.ID == "" || review.IsArchived {
		return domain.ReviewerSurface{}
	}
	handleID := review.ReviewerHandleID
	if review.InterfaceMode == domain.ReviewerInterfaceChat {
		handleID = ""
	}
	return domain.ReviewerSurface{
		Mode: review.InterfaceMode, ReviewID: review.ID, Harness: review.Harness,
		HandleID: handleID, ControllerError: review.ControllerError,
	}
}

func legacyReviewerHandle(review domain.Review) string {
	if review.IsArchived || review.InterfaceMode == domain.ReviewerInterfaceChat {
		return ""
	}
	return review.ReviewerHandleID
}

// Cancel interrupts the live reviewer pane for a worker and marks running
// review runs as cancelled so they no longer block a fresh trigger.
func (e *Engine) Cancel(ctx stdctx.Context, workerID domain.SessionID) (CancelResult, error) {
	if workerID == "" {
		return CancelResult{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return CancelResult{}, err
	}
	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return CancelResult{}, err
	}
	if !ok {
		return CancelResult{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	harness, _, err := e.reviewerSelection(ctx, worker)
	if err != nil {
		return CancelResult{}, err
	}
	running, err := e.store.ListRunningReviewRunsBySession(ctx, workerID)
	if err != nil {
		return CancelResult{}, err
	}
	if len(running) == 0 {
		review, ok, err := e.currentReviewForSession(ctx, workerID, harness)
		if err != nil {
			return CancelResult{}, err
		}
		handle := ""
		if ok {
			handle = review.ReviewerHandleID
		}
		prs, err := e.prs.ListPRsBySession(ctx, workerID)
		if err != nil {
			return CancelResult{}, err
		}
		runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
		if err != nil {
			return CancelResult{}, err
		}
		return CancelResult{ReviewerHandleID: handle, Reviews: Plan(prs, runs)}, nil
	}
	// Several reviewers may be running on this worker at once; cancel stops
	// all of them. The selected reviewer is cancelled first and its handle is
	// the one reported, so single-reviewer clients see what they did before.
	reviewers, err := e.runningReviewersForCancel(ctx, workerID, harness, running)
	if err != nil {
		return CancelResult{}, err
	}
	if len(reviewers) == 0 {
		return CancelResult{}, fmt.Errorf("%w: reviewer for worker session %q", ErrNotFound, workerID)
	}
	cancelledHarness := make(map[domain.ReviewerHarness]bool, len(reviewers))
	for _, review := range reviewers {
		if err := e.launcher.Cancel(ctx, review.ReviewerHandleID, review.Harness); err != nil {
			alive, aliveErr := e.launcher.Alive(ctx, review.ReviewerHandleID, review.ReviewerLaunchID)
			if aliveErr != nil {
				return CancelResult{}, err
			}
			if alive {
				return CancelResult{}, err
			}
		}
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, workerID, review.Harness, "cancelled by user"); err != nil {
			return CancelResult{}, err
		}
		cancelledHarness[review.Harness] = true
	}
	primary := reviewers[0]
	// Legacy rows have no harness; they belong to the reported reviewer.
	hasLegacy := false
	for _, run := range running {
		hasLegacy = hasLegacy || run.Harness == ""
	}
	if hasLegacy {
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, workerID, "", "cancelled by user"); err != nil {
			return CancelResult{}, err
		}
	}
	cancelled := make([]domain.ReviewRun, 0, len(running))
	for _, run := range running {
		if run.Harness != "" && !cancelledHarness[run.Harness] {
			continue
		}
		run.Status = domain.ReviewRunCancelled
		run.Verdict = domain.VerdictNone
		run.Body = "cancelled by user"
		run.GithubReviewID = ""
		cancelled = append(cancelled, run)
	}
	prs, err := e.prs.ListPRsBySession(ctx, workerID)
	if err != nil {
		return CancelResult{}, err
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return CancelResult{}, err
	}
	return CancelResult{ReviewerHandleID: primary.ReviewerHandleID, Reviews: Plan(prs, runs), CancelledRuns: cancelled}, nil
}

// runningReviewersForCancel returns every reviewer row with a live handle that
// owns a running pass, selected reviewer first. Legacy runs with no harness
// resolve to the selected reviewer, matching currentReviewForCancel.
func (e *Engine) runningReviewersForCancel(ctx stdctx.Context, workerID domain.SessionID, selected domain.ReviewerHarness, running []domain.ReviewRun) ([]domain.Review, error) {
	var out []domain.Review
	seen := map[domain.ReviewerHarness]bool{}
	add := func(review domain.Review) {
		if review.ReviewerHandleID == "" || seen[review.Harness] {
			return
		}
		seen[review.Harness] = true
		out = append(out, review)
	}
	primary, ok, err := e.currentReviewForCancel(ctx, workerID, selected, running)
	if err != nil {
		return nil, err
	}
	if ok {
		add(primary)
	}
	for _, run := range running {
		if run.Harness == "" || seen[run.Harness] {
			continue
		}
		review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, run.Harness)
		if err != nil {
			return nil, err
		}
		if ok {
			add(review)
		}
	}
	return out, nil
}

func (e *Engine) currentReviewForCancel(ctx stdctx.Context, workerID domain.SessionID, selected domain.ReviewerHarness, running []domain.ReviewRun) (domain.Review, bool, error) {
	if len(running) > 0 {
		if selected != "" {
			for _, run := range running {
				if run.Harness != selected && run.Harness != "" {
					continue
				}
				review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, selected)
				if err != nil || (ok && review.ReviewerHandleID != "") {
					return review, ok, err
				}
				break
			}
		}
		for _, run := range running {
			if run.Harness == "" || run.Harness == selected {
				continue
			}
			review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, run.Harness)
			if err != nil || (ok && review.ReviewerHandleID != "") {
				return review, ok, err
			}
		}
	}
	return e.currentReviewForSession(ctx, workerID, selected)
}

func (e *Engine) currentReviewForSession(ctx stdctx.Context, workerID domain.SessionID, selected domain.ReviewerHarness) (domain.Review, bool, error) {
	if selected != "" {
		review, ok, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, selected)
		if err != nil || ok {
			return review, ok, err
		}
	}
	return e.store.GetReviewBySession(ctx, workerID)
}

// TerminateReviewer destroys the live reviewer pane for a worker and cancels
// any running review runs. Unlike Cancel, this does not ask the reviewer
// adapter for a graceful interrupt sequence: worker termination/restore must
// remove the terminal pane itself.
func (e *Engine) TerminateReviewer(ctx stdctx.Context, workerID domain.SessionID, body string) (TerminateResult, error) {
	return e.terminateReviewer(ctx, workerID, body, false)
}

// ArchiveReviewer retires all reviewer surfaces but retains conversations and results.
func (e *Engine) ArchiveReviewer(ctx stdctx.Context, workerID domain.SessionID) error {
	_, err := e.terminateReviewer(ctx, workerID, "cancelled because reviewer was archived", true)
	return err
}

func (e *Engine) terminateReviewer(ctx stdctx.Context, workerID domain.SessionID, body string, archive bool) (TerminateResult, error) {
	if workerID == "" {
		return TerminateResult{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if err := e.waitForChatRecovery(ctx); err != nil {
		return TerminateResult{}, err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()
	reviews, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return TerminateResult{}, err
	}
	running, err := e.store.ListRunningReviewRunsBySession(ctx, workerID)
	if err != nil {
		return TerminateResult{}, err
	}
	destroyedHandle := ""
	for _, review := range reviews {
		if archive && review.InterfaceMode == domain.ReviewerInterfaceChat {
			if err := e.stopReviewChatWork(ctx, review); err != nil {
				return TerminateResult{}, err
			}
		} else if review.ReviewerHandleID != "" {
			if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
				return TerminateResult{}, err
			}
		}
		if destroyedHandle == "" {
			destroyedHandle = review.ReviewerHandleID
		}
	}
	if !archive && len(reviews) > 0 {
		if err := e.store.ClearReviewerHandle(ctx, workerID); err != nil {
			return TerminateResult{}, err
		}
	}
	if body == "" {
		body = "cancelled by worker session lifecycle"
	}
	if _, err := e.store.CancelRunningReviewRunsBySession(ctx, workerID, body); err != nil {
		return TerminateResult{}, err
	}
	if archive {
		if err := e.store.ArchiveReviewsBySession(ctx, workerID, e.clock()); err != nil {
			return TerminateResult{}, err
		}
		return TerminateResult{}, nil
	}
	cancelled := make([]domain.ReviewRun, 0, len(running))
	for _, run := range running {
		run.Status = domain.ReviewRunCancelled
		run.Verdict = domain.VerdictNone
		run.Body = body
		run.GithubReviewID = ""
		cancelled = append(cancelled, run)
	}
	return TerminateResult{ReviewerHandleID: destroyedHandle, CancelledRuns: cancelled}, nil
}

// reviewerSelection resolves which reviewer reviews the worker's PR: a
// persisted session preference wins, then the project's reviewer config, then
// the project's default worker config, then the worker's own harness when
// supported, otherwise claude-code.
func (e *Engine) reviewerSelection(
	ctx stdctx.Context,
	worker domain.SessionRecord,
) (domain.ReviewerHarness, domain.AgentConfig, error) {
	if worker.ReviewerHarness != "" || !worker.ReviewerConfig.IsZero() {
		harness := worker.ReviewerHarness
		projectHarness, projectConfig, err := e.projectReviewerSelection(ctx, worker)
		if err != nil {
			return "", domain.AgentConfig{}, err
		}
		if harness == "" {
			harness = projectHarness
		}
		baseConfig := domain.AgentConfig{}
		if harness == projectHarness {
			baseConfig = projectConfig
		}
		return harness, mergeReviewerAgentConfig(baseConfig, worker.ReviewerConfig), nil
	}
	return e.projectReviewerSelection(ctx, worker)
}

func mergeReviewerAgentConfig(base, override domain.AgentConfig) domain.AgentConfig {
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.Effort != "" {
		base.Effort = override.Effort
	}
	if override.Mode != "" {
		base.Mode = override.Mode
	}
	if override.Permissions != "" {
		base.Permissions = override.Permissions
	}
	return base
}

func (e *Engine) projectReviewerSelection(
	ctx stdctx.Context,
	worker domain.SessionRecord,
) (domain.ReviewerHarness, domain.AgentConfig, error) {
	var cfg domain.ProjectConfig
	if e.projects != nil {
		if proj, ok, err := e.projects.GetProject(ctx, string(worker.ProjectID)); err != nil {
			return "", domain.AgentConfig{}, err
		} else if ok {
			cfg = proj.Config
		}
	}
	if len(cfg.Reviewers) > 0 {
		return cfg.Reviewers[0].Harness, cfg.Reviewers[0].AgentConfig, nil
	}
	harness, config := cfg.DefaultWorkerReviewer()
	if harness != "" {
		return harness, config, nil
	}
	return cfg.ResolveReviewerHarness(worker.Harness), domain.AgentConfig{}, nil
}

func (e *Engine) projectEnv(ctx stdctx.Context, id domain.ProjectID) (map[string]string, error) {
	if e.projects == nil {
		return nil, nil
	}
	project, ok, err := e.projects.GetProject(ctx, string(id))
	if err != nil || !ok {
		return nil, err
	}
	return project.Config.Env, nil
}

func (e *Engine) upsertReview(ctx stdctx.Context, worker domain.SessionRecord, harness domain.ReviewerHarness, handleID, agentSessionID, reviewerLaunchID string, activityState domain.ActivityState, now time.Time) (domain.Review, error) {
	existing, ok, err := e.store.GetReviewBySessionAndHarness(ctx, worker.ID, harness)
	if err != nil {
		return domain.Review{}, err
	}
	agentSessionID = strings.TrimSpace(agentSessionID)
	review := domain.Review{
		ID:                    e.newID(),
		SessionID:             worker.ID,
		ProjectID:             worker.ProjectID,
		Harness:               harness,
		PRURL:                 "",
		ReviewerHandleID:      handleID,
		AgentSessionID:        agentSessionID,
		ReviewerLaunchID:      strings.TrimSpace(reviewerLaunchID),
		ReviewerActivityState: activityState,
		InterfaceMode:         domain.ReviewerInterfaceTUI,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if ok {
		// Reuse the existing row's identity and creation time; UpsertReview
		// refreshes harness/pr_url/reviewer_handle_id/updated_at.
		review.ID = existing.ID
		review.IsArchived = existing.IsArchived
		if existing.IsArchived {
			review.ReviewerActivityState = existing.ReviewerActivityState
		}
		review.CreatedAt = existing.CreatedAt
		if review.ReviewerLaunchID == "" {
			review.ReviewerLaunchID = existing.ReviewerLaunchID
		}
		if review.ReviewerActivityState == "" {
			review.ReviewerActivityState = existing.ReviewerActivityState
		}
		review.InterfaceMode = existing.InterfaceMode
		review.ProviderConversationID = existing.ProviderConversationID
		review.ControllerGeneration = existing.ControllerGeneration
		review.ControllerError = existing.ControllerError
	}
	if err := e.store.UpsertReview(ctx, review); err != nil {
		return domain.Review{}, err
	}
	return review, nil
}

func (e *Engine) setReviewerInterfaceMode(ctx stdctx.Context, reviewID string, mode domain.ReviewerInterfaceMode, now time.Time) error {
	ok, err := e.store.SetReviewInterfaceMode(ctx, reviewID, mode, now)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: reviewer %q", ErrNotFound, reviewID)
	}
	return nil
}

// stopReviewChatWork settles durable work even when no controller survived a
// daemon restart. It runs before replacing the review controller fence.
func (e *Engine) stopReviewChatWork(ctx stdctx.Context, review domain.Review) error {
	if review.InterfaceMode != domain.ReviewerInterfaceChat {
		return nil
	}
	if err := e.launcher.Destroy(ctx, reviewerChatHandlePrefix+review.ID); err != nil {
		return fmt.Errorf("stop previous reviewer Chat: %w", err)
	}
	return e.store.SettleReviewChatWork(ctx, review.ID, e.clock())
}
