package review

import (
	stdctx "context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SupervisorConfig holds the run supervisor's time limits. Every active run
// state has an owner and one of these limits, so a run always reaches a final
// state without anyone polling the review list.
type SupervisorConfig struct {
	// TickInterval is how often every active run is evaluated. Activity
	// signals and cancels also wake the supervisor for their session at once.
	TickInterval time.Duration
	// LaunchTimeout bounds launching: the reviewer start or task delivery.
	LaunchTimeout time.Duration
	// FirstActivityWindow is how long a delivered task may wait for the first
	// activity signal from the launch serving it.
	FirstActivityWindow time.Duration
	// IdleGrace is how long an idle reviewer may go without submitting before
	// its turn counts as ended.
	IdleGrace time.Duration
	// MaxDuration bounds a running pass, including time spent waiting for input.
	MaxDuration time.Duration
	// CancelTimeout is how long a cancel waits for the reviewer to stop before
	// the supervisor force-kills it.
	CancelTimeout time.Duration
}

// DefaultSupervisorConfig returns the documented default time limits.
func DefaultSupervisorConfig() SupervisorConfig {
	return SupervisorConfig{
		TickInterval:        15 * time.Second,
		LaunchTimeout:       2 * time.Minute,
		FirstActivityWindow: 2 * time.Minute,
		IdleGrace:           60 * time.Second,
		MaxDuration:         60 * time.Minute,
		CancelTimeout:       30 * time.Second,
	}
}

func (c SupervisorConfig) withDefaults() SupervisorConfig {
	d := DefaultSupervisorConfig()
	if c.TickInterval <= 0 {
		c.TickInterval = d.TickInterval
	}
	if c.LaunchTimeout <= 0 {
		c.LaunchTimeout = d.LaunchTimeout
	}
	if c.FirstActivityWindow <= 0 {
		c.FirstActivityWindow = d.FirstActivityWindow
	}
	if c.IdleGrace <= 0 {
		c.IdleGrace = d.IdleGrace
	}
	if c.MaxDuration <= 0 {
		c.MaxDuration = d.MaxDuration
	}
	if c.CancelTimeout <= 0 {
		c.CancelTimeout = d.CancelTimeout
	}
	return c
}

// activityMark is the latest activity signal the daemon accepted for one
// reviewer launch. It is evidence for the supervisor, never run state.
type activityMark struct {
	launchID string
	state    domain.ActivityState
	at       time.Time
}

type supervisorState struct {
	config SupervisorConfig
	wake   chan domain.SessionID

	mu       sync.Mutex
	activity map[string]activityMark
}

func newSupervisorState(config SupervisorConfig) supervisorState {
	return supervisorState{
		config:   config.withDefaults(),
		wake:     make(chan domain.SessionID, 64),
		activity: make(map[string]activityMark),
	}
}

// activityReporter is implemented by launchers that know which reviewer
// harnesses report activity through hooks. Harnesses that do not are judged
// only by liveness and MaxDuration.
type activityReporter interface {
	ReportsActivity(harness domain.ReviewerHarness) bool
}

var inFlightRunStatuses = []domain.ReviewRunStatus{domain.ReviewRunQueued, domain.ReviewRunLaunching, domain.ReviewRunRunning}

// reviewRunInFlight reports whether a run is active and not being cancelled.
func reviewRunInFlight(status domain.ReviewRunStatus) bool {
	return status == domain.ReviewRunQueued || status == domain.ReviewRunLaunching || status == domain.ReviewRunRunning
}

// transitionRun applies one guarded transition and logs every final state with
// its reason. It reports false when another actor finished the run first.
func (e *Engine) transitionRun(ctx stdctx.Context, run domain.ReviewRun, from []domain.ReviewRunStatus, to domain.ReviewRunStatus, patch domain.ReviewRunPatch) (bool, error) {
	moved, err := e.store.TransitionReviewRun(ctx, run.ID, from, to, patch)
	if err != nil || !moved {
		return moved, err
	}
	if to.IsFinal() {
		slog.Default().InfoContext(ctx, "review run ended", "run", run.ID, "session", run.SessionID, "harness", run.Harness,
			"from", run.Status, "status", to, "failure_reason", patch.FailureReason, "cancelled_by", patch.CancelledBy)
	}
	return true, nil
}

// failRuns moves every still-in-flight run in runs to failed with reason. The
// cause is logged rather than stored: body only ever holds review text.
func (e *Engine) failRuns(ctx stdctx.Context, runs []domain.ReviewRun, reason domain.ReviewFailureReason, cause error) error {
	if len(runs) == 0 {
		return nil
	}
	now := e.clock()
	var errs []error
	for _, run := range runs {
		moved, err := e.transitionRun(ctx, run, inFlightRunStatuses, domain.ReviewRunFailed, domain.ReviewRunPatch{FailureReason: reason, EndedAt: &now})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if moved && cause != nil {
			slog.Default().WarnContext(ctx, "review run failed", "run", run.ID, "session", run.SessionID, "failure_reason", reason, "err", cause)
		}
	}
	return errors.Join(errs...)
}

// markRunsLaunching moves freshly queued runs to launching just before the
// reviewer is started or notified.
func (e *Engine) markRunsLaunching(ctx stdctx.Context, runs []domain.ReviewRun, launchID string) error {
	now := e.clock()
	for _, run := range runs {
		if _, err := e.transitionRun(ctx, run, []domain.ReviewRunStatus{domain.ReviewRunQueued}, domain.ReviewRunLaunching, domain.ReviewRunPatch{LaunchID: launchID, LaunchingAt: &now}); err != nil {
			return err
		}
	}
	return nil
}

// markRunRunning records that the task prompt reached the reviewer, and
// returns the run as stored, which is final if another actor ended it first.
func (e *Engine) markRunRunning(ctx stdctx.Context, run domain.ReviewRun, from []domain.ReviewRunStatus, launchID string, at time.Time) (domain.ReviewRun, error) {
	if _, err := e.transitionRun(ctx, run, from, domain.ReviewRunRunning, domain.ReviewRunPatch{LaunchID: launchID, StartedAt: &at}); err != nil {
		return run, err
	}
	e.forgetActivity(run.ReviewID)
	current, ok, err := e.store.GetReviewRun(ctx, run.ID)
	if err != nil {
		return run, err
	}
	if !ok {
		return run, nil
	}
	return current, nil
}

// supersedeOlderRuns ends a PR's in-flight runs against any older head. A
// run that is already being cancelled finishes its cancel instead.
func (e *Engine) supersedeOlderRuns(ctx stdctx.Context, workerID domain.SessionID, prURL, targetSHA string) error {
	now := e.clock()
	_, err := e.store.TransitionReviewRunsForOlderSHA(ctx, workerID, prURL, targetSHA, inFlightRunStatuses, domain.ReviewRunSuperseded, domain.ReviewRunPatch{EndedAt: &now})
	return err
}

// runEnding is the final state endReviewerRuns gives in-flight runs whose
// reviewer is gone.
type runEnding struct {
	to          domain.ReviewRunStatus
	reason      domain.ReviewFailureReason
	cancelledBy domain.ReviewCancelledBy
}

func endAsCancelled(by domain.ReviewCancelledBy) runEnding {
	return runEnding{to: domain.ReviewRunCancelled, cancelledBy: by}
}

func endAsFailed(reason domain.ReviewFailureReason) runEnding {
	return runEnding{to: domain.ReviewRunFailed, reason: reason}
}

// endReviewerRuns ends a worker's active runs after their reviewer has been
// stopped: a pending cancel is now confirmed, and in-flight runs take ending.
// A nil harness covers every reviewer of the worker.
func (e *Engine) endReviewerRuns(ctx stdctx.Context, workerID domain.SessionID, harness *domain.ReviewerHarness, ending runEnding) error {
	now := e.clock()
	if _, err := e.store.TransitionReviewRunsBySession(ctx, workerID, harness, []domain.ReviewRunStatus{domain.ReviewRunCancelling}, domain.ReviewRunCancelled, domain.ReviewRunPatch{EndedAt: &now}); err != nil {
		return err
	}
	_, err := e.store.TransitionReviewRunsBySession(ctx, workerID, harness, inFlightRunStatuses, ending.to, domain.ReviewRunPatch{
		FailureReason: ending.reason,
		CancelledBy:   ending.cancelledBy,
		EndedAt:       &now,
	})
	return err
}

// ObserveReviewActivity feeds an accepted reviewer activity signal to the run
// supervisor: it records the first activity of the launch serving a delivered
// run, remembers when the reviewer went idle, and wakes the supervisor for
// the worker so it reacts without waiting for the next tick.
func (e *Engine) ObserveReviewActivity(ctx stdctx.Context, review domain.Review, launchID string, state domain.ActivityState) error {
	if review.ID == "" || state == "" {
		return nil
	}
	now := e.clock()
	e.supervisor.mu.Lock()
	e.supervisor.activity[review.ID] = activityMark{launchID: launchID, state: state, at: now}
	e.supervisor.mu.Unlock()
	if launchID != "" {
		switch state {
		case domain.ActivityActive, domain.ActivityBlocked, domain.ActivityWaitingInput:
			if _, err := e.store.MarkReviewRunsFirstActivity(ctx, review.ID, launchID, now); err != nil {
				return err
			}
		}
	}
	e.wakeSupervisor(review.SessionID)
	return nil
}

func (e *Engine) forgetActivity(reviewID string) {
	e.supervisor.mu.Lock()
	delete(e.supervisor.activity, reviewID)
	e.supervisor.mu.Unlock()
}

// activitySince returns when the reviewer entered state, as far as this
// daemon observed. A state read from the review row with no signal seen
// since startup counts from now.
func (e *Engine) activitySince(review domain.Review, state domain.ActivityState, now time.Time) time.Time {
	e.supervisor.mu.Lock()
	defer e.supervisor.mu.Unlock()
	mark, ok := e.supervisor.activity[review.ID]
	if ok && mark.state == state && mark.launchID == review.ReviewerLaunchID {
		return mark.at
	}
	e.supervisor.activity[review.ID] = activityMark{launchID: review.ReviewerLaunchID, state: state, at: now}
	return now
}

func (e *Engine) wakeSupervisor(workerID domain.SessionID) {
	select {
	case e.supervisor.wake <- workerID:
	default:
	}
}

func (e *Engine) tracksActivity(review domain.Review) bool {
	if review.InterfaceMode == domain.ReviewerInterfaceChat {
		// Chat turn settlement ends a Chat reviewer's runs directly.
		return false
	}
	reporter, ok := e.launcher.(activityReporter)
	return ok && reporter.ReportsActivity(review.Harness)
}

// RunSupervisor owns every active review run until the daemon stops: it
// evaluates them on each tick and whenever a reviewer reports activity or a
// cancel is requested. It returns when ctx is done.
func (e *Engine) RunSupervisor(ctx stdctx.Context) {
	if err := e.waitForChatRecovery(ctx); err != nil {
		return
	}
	ticker := time.NewTicker(e.supervisor.config.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.EvaluateAll(ctx); err != nil && ctx.Err() == nil {
				slog.Default().WarnContext(ctx, "review run supervisor tick failed", "err", err)
			}
		case workerID := <-e.supervisor.wake:
			if unlock, ok := e.tryLockWorker(workerID); ok {
				_, err := e.evaluateSessionLocked(ctx, workerID)
				unlock()
				if err != nil && ctx.Err() == nil {
					slog.Default().WarnContext(ctx, "review run supervisor evaluation failed", "session", workerID, "err", err)
				}
			}
		}
	}
}

// EvaluateAll applies the supervisor's rules to every active run. A worker
// whose trigger is still launching is skipped, except that a launch hung past
// LaunchTimeout is failed without waiting for that call to return.
func (e *Engine) EvaluateAll(ctx stdctx.Context) error {
	runs, err := e.store.ListActiveReviewRuns(ctx)
	if err != nil {
		return err
	}
	bySession := map[domain.SessionID][]domain.ReviewRun{}
	var sessions []domain.SessionID
	for _, run := range runs {
		if _, seen := bySession[run.SessionID]; !seen {
			sessions = append(sessions, run.SessionID)
		}
		bySession[run.SessionID] = append(bySession[run.SessionID], run)
	}
	var errs []error
	for _, workerID := range sessions {
		unlock, ok := e.tryLockWorker(workerID)
		if !ok {
			errs = append(errs, e.failHungLaunches(ctx, bySession[workerID]))
			continue
		}
		_, err := e.evaluateSessionLocked(ctx, workerID)
		unlock()
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// EvaluateSession settles one worker's active runs now. Trigger, Cancel and
// List call the locked form so they never act on a run the supervisor would
// end on its next tick.
func (e *Engine) EvaluateSession(ctx stdctx.Context, workerID domain.SessionID) error {
	unlock := e.lockWorker(workerID)
	defer unlock()
	_, err := e.evaluateSessionLocked(ctx, workerID)
	return err
}

func (e *Engine) failHungLaunches(ctx stdctx.Context, runs []domain.ReviewRun) error {
	now := e.clock()
	var hung []domain.ReviewRun
	for _, run := range runs {
		if run.Status == domain.ReviewRunLaunching && elapsed(now, launchingSince(run)) >= e.supervisor.config.LaunchTimeout {
			hung = append(hung, run)
		}
	}
	return e.failRuns(ctx, hung, domain.ReviewFailureNeverStarted, errors.New("reviewer launch did not finish within the launch timeout"))
}

// supervisorDecision is what the supervisor does with one run on one pass.
type supervisorDecision struct {
	to        domain.ReviewRunStatus
	patch     domain.ReviewRunPatch
	interrupt bool
}

// evaluateSessionLocked applies the supervisor's transition rules to one
// worker's active runs. The caller holds the worker lock. It reports whether
// any run moved.
func (e *Engine) evaluateSessionLocked(ctx stdctx.Context, workerID domain.SessionID) (bool, error) {
	runs, err := e.store.ListActiveReviewRunsBySession(ctx, workerID)
	if err != nil || len(runs) == 0 {
		return false, err
	}
	reviews, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return false, err
	}
	lookup := newReviewLookup(reviews)
	probes := map[string]reviewerProbe{}
	now := e.clock()
	changed := false
	exited := map[string]domain.Review{}
	interrupt := map[string]domain.Review{}
	var errs []error
	for _, run := range runs {
		review, ok := lookup.find(run)
		if !ok {
			// No reviewer row is no evidence either way: only time limits apply.
			review = domain.Review{ID: "unknown:" + run.ID, ReviewerHandleID: "unknown"}
			probes[review.ID] = reviewerProbe{alive: true, known: false}
		}
		decision, err := e.decide(ctx, run, review, now, probes)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if decision.to == "" {
			continue
		}
		moved, err := e.transitionRun(ctx, run, []domain.ReviewRunStatus{run.Status}, decision.to, decision.patch)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		changed = changed || moved
		if moved && ok && decision.interrupt && review.ReviewerHandleID != "" {
			interrupt[review.ID] = review
		}
		if moved && ok && review.ReviewerHandleID != "" && review.ReviewerActivityState != domain.ActivityExited &&
			(decision.patch.FailureReason == domain.ReviewFailureProcessExited || decision.patch.FailureReason == domain.ReviewFailureExitedDuringLaunch) {
			exited[review.ID] = review
		}
	}
	for _, review := range exited {
		// The process is definitively gone; say so on the reviewer row so
		// clients stop offering to open it and offer Restore instead.
		review.ReviewerActivityState = domain.ActivityExited
		review.UpdatedAt = now
		if err := e.store.UpsertReview(ctx, review); err != nil {
			errs = append(errs, err)
		}
	}
	if len(interrupt) > 0 {
		// Stop a reviewer still working on an ended pass, unless it is still
		// serving another active pass.
		remaining, err := e.store.ListActiveReviewRunsBySession(ctx, workerID)
		if err != nil {
			errs = append(errs, err)
		}
		for _, run := range remaining {
			delete(interrupt, run.ReviewID)
		}
		for _, review := range interrupt {
			if err := e.launcher.Cancel(ctx, review.ReviewerHandleID, review.Harness); err != nil {
				slog.Default().WarnContext(ctx, "interrupt reviewer after its pass ended", "review", review.ID, "err", err)
			}
		}
	}
	return changed, errors.Join(errs...)
}

// reviewerProbe is one liveness answer per reviewer per evaluation. known is
// false when the probe failed: an unknown runtime state is never proof that
// the reviewer exited.
type reviewerProbe struct {
	alive bool
	known bool
}

func (e *Engine) probeReviewer(ctx stdctx.Context, review domain.Review, probes map[string]reviewerProbe) reviewerProbe {
	handle := reviewerProbeHandle(review)
	if handle == "" {
		return reviewerProbe{alive: false, known: true}
	}
	if probe, ok := probes[review.ID]; ok {
		return probe
	}
	alive, err := e.launcher.Alive(ctx, handle, review.ReviewerLaunchID)
	probe := reviewerProbe{alive: alive, known: err == nil}
	if err != nil {
		probe.alive = true
	}
	probes[review.ID] = probe
	return probe
}

// reviewerProbeHandle is the handle that answers whether a reviewer is alive.
// A Chat reviewer is addressed by its review id even when no handle is stored.
func reviewerProbeHandle(review domain.Review) string {
	switch {
	case review.ID == "":
		return ""
	case review.InterfaceMode == domain.ReviewerInterfaceChat:
		return reviewerChatHandlePrefix + review.ID
	default:
		return review.ReviewerHandleID
	}
}

func (e *Engine) decide(ctx stdctx.Context, run domain.ReviewRun, review domain.Review, now time.Time, probes map[string]reviewerProbe) (supervisorDecision, error) {
	cfg := e.supervisor.config
	fail := func(reason domain.ReviewFailureReason, interrupt bool) supervisorDecision {
		return supervisorDecision{to: domain.ReviewRunFailed, patch: domain.ReviewRunPatch{FailureReason: reason, EndedAt: &now}, interrupt: interrupt}
	}
	switch run.Status {
	case domain.ReviewRunQueued:
		// A trigger moves its runs on within the same call while holding this
		// worker's lock; one still queued here was left by a call that is gone.
		return fail(domain.ReviewFailureLaunchFailed, false), nil
	case domain.ReviewRunLaunching:
		if elapsed(now, launchingSince(run)) >= cfg.LaunchTimeout {
			return fail(domain.ReviewFailureNeverStarted, true), nil
		}
		if review.ReviewerHandleID != "" {
			if probe := e.probeReviewer(ctx, review, probes); probe.known && !probe.alive {
				return fail(domain.ReviewFailureExitedDuringLaunch, false), nil
			}
		}
		return supervisorDecision{}, nil
	case domain.ReviewRunRunning:
		if probe := e.probeReviewer(ctx, review, probes); probe.known && !probe.alive {
			return fail(domain.ReviewFailureProcessExited, false), nil
		}
		started := startedSince(run)
		if elapsed(now, started) >= cfg.MaxDuration {
			return fail(domain.ReviewFailureTimedOut, true), nil
		}
		if !e.tracksActivity(review) {
			return supervisorDecision{}, nil
		}
		if run.FirstActivityAt == nil {
			if elapsed(now, started) >= cfg.FirstActivityWindow {
				return fail(domain.ReviewFailureNeverStarted, true), nil
			}
			return supervisorDecision{}, nil
		}
		if review.ReviewerActivityState == domain.ActivityIdle {
			idleSince := e.activitySince(review, domain.ActivityIdle, now)
			if idleSince.Before(*run.FirstActivityAt) {
				idleSince = *run.FirstActivityAt
			}
			if elapsed(now, idleSince) >= cfg.IdleGrace {
				return fail(domain.ReviewFailureTurnEndedWithoutSubmit, false), nil
			}
		}
		return supervisorDecision{}, nil
	case domain.ReviewRunCancelling:
		confirmed := supervisorDecision{to: domain.ReviewRunCancelled, patch: domain.ReviewRunPatch{EndedAt: &now}}
		if probe := e.probeReviewer(ctx, review, probes); probe.known && !probe.alive {
			return confirmed, nil
		}
		requested := run.CreatedAt
		if run.CancelRequestedAt != nil {
			requested = *run.CancelRequestedAt
		}
		if e.tracksActivity(review) && e.turnStoppedSince(review, requested) {
			return confirmed, nil
		}
		if elapsed(now, requested) < cfg.CancelTimeout {
			return supervisorDecision{}, nil
		}
		return e.forceKill(ctx, run, review, now, probes)
	default:
		return supervisorDecision{}, nil
	}
}

// turnStoppedSince reports whether the reviewer reported it stopped working
// after the cancel was requested.
func (e *Engine) turnStoppedSince(review domain.Review, requested time.Time) bool {
	e.supervisor.mu.Lock()
	defer e.supervisor.mu.Unlock()
	mark, ok := e.supervisor.activity[review.ID]
	if !ok || mark.launchID != review.ReviewerLaunchID || mark.at.Before(requested) {
		return false
	}
	return mark.state == domain.ActivityIdle || mark.state == domain.ActivityWaitingInput || mark.state == domain.ActivityExited
}

// forceKill destroys a reviewer that ignored its interrupt for longer than
// the cancel timeout. The run is cancelled when the kill is confirmed and
// failed with cancel_unconfirmed otherwise.
func (e *Engine) forceKill(ctx stdctx.Context, run domain.ReviewRun, review domain.Review, now time.Time, probes map[string]reviewerProbe) (supervisorDecision, error) {
	unconfirmed := supervisorDecision{to: domain.ReviewRunFailed, patch: domain.ReviewRunPatch{FailureReason: domain.ReviewFailureCancelUnconfirmed, EndedAt: &now}}
	if review.ReviewerHandleID == "" {
		return supervisorDecision{to: domain.ReviewRunCancelled, patch: domain.ReviewRunPatch{EndedAt: &now}}, nil
	}
	if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
		slog.Default().WarnContext(ctx, "force-kill reviewer after cancel timeout", "run", run.ID, "review", review.ID, "err", err)
		return unconfirmed, nil
	}
	delete(probes, review.ID)
	if probe := e.probeReviewer(ctx, review, probes); !probe.known || probe.alive {
		return unconfirmed, nil
	}
	if err := e.store.ClearReviewerHandleByHarness(ctx, run.SessionID, review.Harness); err != nil {
		return supervisorDecision{}, err
	}
	return supervisorDecision{to: domain.ReviewRunCancelled, patch: domain.ReviewRunPatch{EndedAt: &now}}, nil
}

// elapsed is how long ago since was. An unknown (zero) reference time counts
// as no time at all: a run is never failed on a clock it does not have.
func elapsed(now, since time.Time) time.Duration {
	if since.IsZero() {
		return 0
	}
	return now.Sub(since)
}

// reviewLookup finds the reviewer row serving a run. Legacy runs predate
// per-harness runs and review ids; they belong to the worker's reviewer that
// still has a pane, as they always have.
type reviewLookup struct {
	byID      map[string]domain.Review
	byHarness map[domain.ReviewerHarness]domain.Review
	fallback  domain.Review
	ok        bool
}

func newReviewLookup(reviews []domain.Review) reviewLookup {
	l := reviewLookup{byID: make(map[string]domain.Review, len(reviews)), byHarness: make(map[domain.ReviewerHarness]domain.Review, len(reviews))}
	for _, review := range reviews {
		l.byID[review.ID] = review
		l.byHarness[review.Harness] = review
		if !l.ok || (l.fallback.ReviewerHandleID == "" && review.ReviewerHandleID != "") {
			l.fallback, l.ok = review, true
		}
	}
	return l
}

func (l reviewLookup) find(run domain.ReviewRun) (domain.Review, bool) {
	if review, ok := l.byID[run.ReviewID]; ok && run.ReviewID != "" {
		return review, true
	}
	if review, ok := l.byHarness[run.Harness]; ok && run.Harness != "" {
		return review, true
	}
	if run.Harness == "" {
		return l.fallback, l.ok
	}
	return domain.Review{}, false
}

func launchingSince(run domain.ReviewRun) time.Time {
	if run.LaunchingAt != nil {
		return *run.LaunchingAt
	}
	return run.CreatedAt
}

func startedSince(run domain.ReviewRun) time.Time {
	if run.StartedAt != nil {
		return *run.StartedAt
	}
	return launchingSince(run)
}

// SweepActiveRunsAfterRestart settles every run the previous daemon left
// active. It runs once at startup, after RecoverChatReviewers and before the
// supervisor. A terminal reviewer whose exact launch is still alive is kept
// for the supervisor; every other active run fails with daemon_restarted and
// its reviewer handle is destroyed, which reaps an orphaned process tree.
func (e *Engine) SweepActiveRunsAfterRestart(ctx stdctx.Context) error {
	runs, err := e.store.ListActiveReviewRuns(ctx)
	if err != nil {
		return err
	}
	bySession := map[domain.SessionID][]domain.ReviewRun{}
	for _, run := range runs {
		bySession[run.SessionID] = append(bySession[run.SessionID], run)
	}
	sessions := make([]domain.SessionID, 0, len(bySession))
	for id := range bySession {
		sessions = append(sessions, id)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i] < sessions[j] })
	var errs []error
	for _, workerID := range sessions {
		unlock := e.lockWorker(workerID)
		errs = append(errs, e.sweepSessionLocked(ctx, workerID, bySession[workerID]))
		unlock()
	}
	return errors.Join(errs...)
}

func (e *Engine) sweepSessionLocked(ctx stdctx.Context, workerID domain.SessionID, runs []domain.ReviewRun) error {
	reviews, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return err
	}
	lookup := newReviewLookup(reviews)
	now := e.clock()
	probes := map[string]reviewerProbe{}
	kept := map[string]bool{}
	orphaned := map[string]domain.Review{}
	var errs []error
	for _, run := range runs {
		review, ok := lookup.find(run)
		if ok && e.readoptable(ctx, run, review, probes) {
			kept[review.ID] = true
			continue
		}
		to, patch := domain.ReviewRunFailed, domain.ReviewRunPatch{FailureReason: domain.ReviewFailureDaemonRestarted, EndedAt: &now}
		if run.Status == domain.ReviewRunCancelling {
			// The reviewer did not survive the restart, so the stop is confirmed.
			to, patch = domain.ReviewRunCancelled, domain.ReviewRunPatch{EndedAt: &now}
		}
		if _, err := e.transitionRun(ctx, run, []domain.ReviewRunStatus{run.Status}, to, patch); err != nil {
			errs = append(errs, err)
			continue
		}
		if ok && review.ReviewerHandleID != "" && review.InterfaceMode != domain.ReviewerInterfaceChat {
			orphaned[review.ID] = review
		}
	}
	for id, review := range orphaned {
		if kept[id] {
			continue
		}
		if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := e.store.ClearReviewerHandleByHarness(ctx, workerID, review.Harness); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// readoptable reports whether the supervisor can keep owning a run across a
// daemon restart. Runs that never reached a reviewer cannot be: their
// trigger call died with the daemon. A Chat run survives when
// RecoverChatReviewers kept its controller; a terminal run when the exact
// launch serving it is still alive. An unknown probe keeps the run, bounded
// by MaxDuration.
func (e *Engine) readoptable(ctx stdctx.Context, run domain.ReviewRun, review domain.Review, probes map[string]reviewerProbe) bool {
	if run.Status != domain.ReviewRunRunning && run.Status != domain.ReviewRunCancelling {
		return false
	}
	if review.InterfaceMode != domain.ReviewerInterfaceChat && (run.LaunchID == "" || run.LaunchID != review.ReviewerLaunchID) {
		return false
	}
	probe := e.probeReviewer(ctx, review, probes)
	return probe.alive
}
