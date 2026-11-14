package review

import (
	stdctx "context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// RunLifecycleConfig holds the time limits of review runs. Every active run
// state has one, so a run always reaches a final state. The runtime reaper
// drives evaluation: each of its cycles observes the reviewers serving an
// in-progress run and hands the pass to ApplyReviewerObservations.
type RunLifecycleConfig struct {
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
	// the reviewer is force-killed.
	CancelTimeout time.Duration
	// SuspendGap is the longest pause between two reaper passes that still
	// counts as the daemon running. A longer one (the machine slept, or the
	// daemon was stalled) is not counted against any run's time limits.
	SuspendGap time.Duration
}

// DefaultRunLifecycleConfig returns the documented default time limits.
func DefaultRunLifecycleConfig() RunLifecycleConfig {
	return RunLifecycleConfig{
		LaunchTimeout:       2 * time.Minute,
		FirstActivityWindow: 2 * time.Minute,
		IdleGrace:           60 * time.Second,
		MaxDuration:         60 * time.Minute,
		CancelTimeout:       30 * time.Second,
		SuspendGap:          time.Minute,
	}
}

func (c RunLifecycleConfig) withDefaults() RunLifecycleConfig {
	d := DefaultRunLifecycleConfig()
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
	if c.SuspendGap <= 0 {
		c.SuspendGap = d.SuspendGap
	}
	return c
}

// activityMark is the latest activity signal the daemon accepted for one
// reviewer launch. It is evidence for the run lifecycle, never run state.
type activityMark struct {
	launchID string
	state    domain.ActivityState
	at       time.Time
}

// suspendGap is a stretch of wall-clock time the daemon did not observe, which
// no run's time limit counts.
type suspendGap struct {
	from, to time.Time
}

type runLifecycleState struct {
	config RunLifecycleConfig

	mu           sync.Mutex
	activity     map[string]activityMark
	lastObserved time.Time
	gaps         []suspendGap
}

func newRunLifecycleState(config RunLifecycleConfig) runLifecycleState {
	return runLifecycleState{
		config:   config.withDefaults(),
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

// ObserveReviewActivity reduces an accepted reviewer activity signal: it
// records the first activity of the launch serving a delivered run, remembers
// when the reviewer went idle, and settles that worker's runs at once instead
// of waiting for the next reaper pass. A worker whose trigger is launching is
// left to that pass.
func (e *Engine) ObserveReviewActivity(ctx stdctx.Context, review domain.Review, launchID string, state domain.ActivityState) error {
	if review.ID == "" || state == "" {
		return nil
	}
	now := e.clock()
	e.runs.mu.Lock()
	e.runs.activity[review.ID] = activityMark{launchID: launchID, state: state, at: now}
	e.runs.mu.Unlock()
	if launchID != "" {
		switch state {
		case domain.ActivityActive, domain.ActivityBlocked, domain.ActivityWaitingInput:
			if _, err := e.store.MarkReviewRunsFirstActivity(ctx, review.ID, launchID, now); err != nil {
				return err
			}
		}
	}
	if unlock, ok := e.tryLockWorker(review.SessionID); ok {
		defer unlock()
		if _, err := e.evaluateSessionLocked(ctx, review.SessionID); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) forgetActivity(reviewID string) {
	e.runs.mu.Lock()
	delete(e.runs.activity, reviewID)
	e.runs.mu.Unlock()
}

// activitySince returns when the reviewer entered state, as far as this
// daemon observed. A state read from the review row with no signal seen
// since startup counts from now.
func (e *Engine) activitySince(review domain.Review, state domain.ActivityState, now time.Time) time.Time {
	e.runs.mu.Lock()
	defer e.runs.mu.Unlock()
	mark, ok := e.runs.activity[review.ID]
	if ok && mark.state == state && mark.launchID == review.ReviewerLaunchID {
		return mark.at
	}
	e.runs.activity[review.ID] = activityMark{launchID: review.ReviewerLaunchID, state: state, at: now}
	return now
}

func (e *Engine) tracksActivity(review domain.Review) bool {
	if review.InterfaceMode == domain.ReviewerInterfaceChat {
		// Chat turn settlement ends a Chat reviewer's runs directly.
		return false
	}
	reporter, ok := e.launcher.(activityReporter)
	return ok && reporter.ReportsActivity(review.Harness)
}

// ListObservableReviewers names the reviewers the runtime reaper should probe:
// only those serving an in-progress run. With no review in progress it is a
// single indexed query that returns nothing. A worker whose in-progress runs
// have no reviewer row gets a handle-less target so its runs are still
// judged on their time limits.
func (e *Engine) ListObservableReviewers(ctx stdctx.Context) ([]ports.ReviewerTarget, error) {
	runs, err := e.store.ListActiveReviewRuns(ctx)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	bySession := groupRunsBySession(runs)
	var targets []ports.ReviewerTarget
	for _, workerID := range bySession.order {
		reviews, err := e.store.ListReviewsBySession(ctx, workerID)
		if err != nil {
			return nil, err
		}
		lookup := newReviewLookup(reviews)
		seen := map[string]bool{}
		orphaned := false
		for _, run := range bySession.runs[workerID] {
			review, ok := lookup.find(run)
			if !ok {
				orphaned = true
				continue
			}
			if seen[review.ID] {
				continue
			}
			seen[review.ID] = true
			targets = append(targets, ports.ReviewerTarget{ReviewID: review.ID, SessionID: workerID, HandleID: reviewerProbeHandle(review), LaunchID: review.ReviewerLaunchID})
		}
		if orphaned && len(seen) == 0 {
			targets = append(targets, ports.ReviewerTarget{SessionID: workerID})
		}
	}
	return targets, nil
}

// ProbeReviewer is the reaper's liveness probe for one reviewer: the exact
// supervised launch when the runtime records one, the Chat controller for a
// Chat reviewer.
func (e *Engine) ProbeReviewer(ctx stdctx.Context, target ports.ReviewerTarget) (bool, error) {
	return e.launcher.Alive(ctx, target.HandleID, target.LaunchID)
}

// ApplyReviewerObservations reduces one reaper pass into review-run
// transitions. The boot pass settles runs a previous daemon left in progress;
// every later pass applies the time limits and liveness rules. A worker whose
// trigger still holds its lock is skipped, except that a launch hung past
// LaunchTimeout is failed without waiting for that call to return.
func (e *Engine) ApplyReviewerObservations(ctx stdctx.Context, pass ports.ReviewerObservationPass) error {
	if err := e.waitForChatRecovery(ctx); err != nil {
		return err
	}
	e.noteObservation(e.clock())
	probes := observedProbes(pass)
	runs, err := e.store.ListActiveReviewRuns(ctx)
	if err != nil {
		return err
	}
	bySession := groupRunsBySession(runs)
	var errs []error
	for _, workerID := range bySession.order {
		if pass.Boot {
			unlock := e.lockWorker(workerID)
			errs = append(errs, e.sweepSessionLocked(ctx, workerID, bySession.runs[workerID], probes[workerID]))
			unlock()
			continue
		}
		unlock, ok := e.tryLockWorker(workerID)
		if !ok {
			errs = append(errs, e.failHungLaunches(ctx, bySession.runs[workerID]))
			continue
		}
		_, err := e.evaluateSessionWith(ctx, workerID, probes[workerID])
		unlock()
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// observedProbes turns a reaper pass into per-worker probe answers keyed by
// review id. A failed probe is unknown, never dead.
func observedProbes(pass ports.ReviewerObservationPass) map[domain.SessionID]map[string]reviewerProbe {
	out := map[domain.SessionID]map[string]reviewerProbe{}
	for _, obs := range pass.Observations {
		if obs.Target.ReviewID == "" {
			continue
		}
		if out[obs.Target.SessionID] == nil {
			out[obs.Target.SessionID] = map[string]reviewerProbe{}
		}
		probe := reviewerProbe{alive: true, known: false}
		switch obs.Reviewer {
		case ports.ProbeAlive:
			probe = reviewerProbe{alive: true, known: true}
		case ports.ProbeDead:
			probe = reviewerProbe{alive: false, known: true}
		}
		out[obs.Target.SessionID][obs.Target.ReviewID] = probe
	}
	return out
}

type sessionRuns struct {
	order []domain.SessionID
	runs  map[domain.SessionID][]domain.ReviewRun
}

func groupRunsBySession(runs []domain.ReviewRun) sessionRuns {
	out := sessionRuns{runs: map[domain.SessionID][]domain.ReviewRun{}}
	for _, run := range runs {
		if _, seen := out.runs[run.SessionID]; !seen {
			out.order = append(out.order, run.SessionID)
		}
		out.runs[run.SessionID] = append(out.runs[run.SessionID], run)
	}
	return out
}

// noteObservation records a pause between reaper passes longer than
// SuspendGap, so that time is not counted against any run.
func (e *Engine) noteObservation(now time.Time) {
	e.runs.mu.Lock()
	defer e.runs.mu.Unlock()
	last := e.runs.lastObserved
	e.runs.lastObserved = now
	if last.IsZero() || now.Sub(last) <= e.runs.config.SuspendGap {
		return
	}
	e.runs.gaps = append(e.runs.gaps, suspendGap{from: last, to: now})
	// Only gaps that can still overlap an active run matter.
	horizon := now.Add(-2 * e.runs.config.MaxDuration)
	kept := e.runs.gaps[:0]
	for _, gap := range e.runs.gaps {
		if gap.to.After(horizon) {
			kept = append(kept, gap)
		}
	}
	e.runs.gaps = kept
}

// EvaluateSession settles one worker's active runs now. Trigger, Cancel and
// List call the locked form so they never act on a run the next reaper pass
// would end.
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
		if run.Status == domain.ReviewRunLaunching && e.elapsed(now, launchingSince(run)) >= e.runs.config.LaunchTimeout {
			hung = append(hung, run)
		}
	}
	return e.failRuns(ctx, hung, domain.ReviewFailureNeverStarted, errors.New("reviewer launch did not finish within the launch timeout"))
}

// runDecision is what the reducer does with one run on one pass.
type runDecision struct {
	to        domain.ReviewRunStatus
	patch     domain.ReviewRunPatch
	interrupt bool
}

// evaluateSessionLocked applies the run lifecycle rules to one worker's
// active runs, probing each reviewer itself. The caller holds the worker lock.
// It reports whether any run moved.
func (e *Engine) evaluateSessionLocked(ctx stdctx.Context, workerID domain.SessionID) (bool, error) {
	return e.evaluateSessionWith(ctx, workerID, nil)
}

// evaluateSessionWith is evaluateSessionLocked with the reaper's probe answers
// already in hand; a reviewer missing from observed is probed directly.
func (e *Engine) evaluateSessionWith(ctx stdctx.Context, workerID domain.SessionID, observed map[string]reviewerProbe) (bool, error) {
	runs, err := e.store.ListActiveReviewRunsBySession(ctx, workerID)
	if err != nil || len(runs) == 0 {
		return false, err
	}
	reviews, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return false, err
	}
	lookup := newReviewLookup(reviews)
	probes := make(map[string]reviewerProbe, len(observed))
	for id, probe := range observed {
		probes[id] = probe
	}
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

func (e *Engine) decide(ctx stdctx.Context, run domain.ReviewRun, review domain.Review, now time.Time, probes map[string]reviewerProbe) (runDecision, error) {
	cfg := e.runs.config
	fail := func(reason domain.ReviewFailureReason, interrupt bool) runDecision {
		return runDecision{to: domain.ReviewRunFailed, patch: domain.ReviewRunPatch{FailureReason: reason, EndedAt: &now}, interrupt: interrupt}
	}
	switch run.Status {
	case domain.ReviewRunQueued:
		// A trigger moves its runs on within the same call while holding this
		// worker's lock; one still queued here was left by a call that is gone.
		return fail(domain.ReviewFailureLaunchFailed, false), nil
	case domain.ReviewRunLaunching:
		if e.elapsed(now, launchingSince(run)) >= cfg.LaunchTimeout {
			return fail(domain.ReviewFailureNeverStarted, true), nil
		}
		if review.ReviewerHandleID != "" {
			if probe := e.probeReviewer(ctx, review, probes); probe.known && !probe.alive {
				return fail(domain.ReviewFailureExitedDuringLaunch, false), nil
			}
		}
		return runDecision{}, nil
	case domain.ReviewRunRunning:
		if probe := e.probeReviewer(ctx, review, probes); probe.known && !probe.alive {
			return fail(domain.ReviewFailureProcessExited, false), nil
		}
		started := startedSince(run)
		if e.elapsed(now, started) >= cfg.MaxDuration {
			return fail(domain.ReviewFailureTimedOut, true), nil
		}
		if !e.tracksActivity(review) {
			return runDecision{}, nil
		}
		if run.FirstActivityAt == nil {
			if e.elapsed(now, started) >= cfg.FirstActivityWindow {
				return fail(domain.ReviewFailureNeverStarted, true), nil
			}
			return runDecision{}, nil
		}
		if review.ReviewerActivityState == domain.ActivityIdle {
			idleSince := e.activitySince(review, domain.ActivityIdle, now)
			if idleSince.Before(*run.FirstActivityAt) {
				idleSince = *run.FirstActivityAt
			}
			if e.elapsed(now, idleSince) >= cfg.IdleGrace {
				return fail(domain.ReviewFailureTurnEndedWithoutSubmit, false), nil
			}
		}
		return runDecision{}, nil
	case domain.ReviewRunCancelling:
		confirmed := runDecision{to: domain.ReviewRunCancelled, patch: domain.ReviewRunPatch{EndedAt: &now}}
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
		if e.elapsed(now, requested) < cfg.CancelTimeout {
			return runDecision{}, nil
		}
		return e.forceKill(ctx, run, review, now, probes)
	default:
		return runDecision{}, nil
	}
}

// turnStoppedSince reports whether the reviewer reported it stopped working
// after the cancel was requested.
func (e *Engine) turnStoppedSince(review domain.Review, requested time.Time) bool {
	e.runs.mu.Lock()
	defer e.runs.mu.Unlock()
	mark, ok := e.runs.activity[review.ID]
	if !ok || mark.launchID != review.ReviewerLaunchID || mark.at.Before(requested) {
		return false
	}
	return mark.state == domain.ActivityIdle || mark.state == domain.ActivityWaitingInput || mark.state == domain.ActivityExited
}

// forceKill destroys a reviewer that ignored its interrupt for longer than
// the cancel timeout. The run is cancelled when the kill is confirmed and
// failed with cancel_unconfirmed otherwise.
func (e *Engine) forceKill(ctx stdctx.Context, run domain.ReviewRun, review domain.Review, now time.Time, probes map[string]reviewerProbe) (runDecision, error) {
	unconfirmed := runDecision{to: domain.ReviewRunFailed, patch: domain.ReviewRunPatch{FailureReason: domain.ReviewFailureCancelUnconfirmed, EndedAt: &now}}
	if review.ReviewerHandleID == "" {
		return runDecision{to: domain.ReviewRunCancelled, patch: domain.ReviewRunPatch{EndedAt: &now}}, nil
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
		return runDecision{}, err
	}
	return runDecision{to: domain.ReviewRunCancelled, patch: domain.ReviewRunPatch{EndedAt: &now}}, nil
}

// elapsed is how long ago since was, not counting time the daemon did not
// observe (the machine slept). An unknown (zero) reference time counts as no
// time at all: a run is never failed on a clock it does not have.
func (e *Engine) elapsed(now, since time.Time) time.Duration {
	if since.IsZero() {
		return 0
	}
	d := now.Sub(since)
	e.runs.mu.Lock()
	defer e.runs.mu.Unlock()
	for _, gap := range e.runs.gaps {
		from, to := gap.from, gap.to
		if from.Before(since) {
			from = since
		}
		if to.After(now) {
			to = now
		}
		if to.After(from) {
			d -= to.Sub(from)
		}
	}
	return d
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

// sweepSessionLocked is the boot pass for one worker: it settles the runs a
// previous daemon left active. A terminal reviewer whose exact launch is
// still alive keeps its runs; every other active run fails with
// daemon_restarted and its reviewer handle is destroyed, which reaps an
// orphaned process tree. observed holds the reaper's probe answers.
func (e *Engine) sweepSessionLocked(ctx stdctx.Context, workerID domain.SessionID, runs []domain.ReviewRun, observed map[string]reviewerProbe) error {
	reviews, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return err
	}
	lookup := newReviewLookup(reviews)
	now := e.clock()
	probes := make(map[string]reviewerProbe, len(observed))
	for id, probe := range observed {
		probes[id] = probe
	}
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

// readoptable reports whether a run can keep its reviewer across a
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
