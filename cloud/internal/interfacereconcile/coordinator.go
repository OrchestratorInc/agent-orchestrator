// Package interfacereconcile converges a durable interface handoff to a single
// committed session interface. HTTP handlers create the durable intent; every
// worker-facing stop/start happens here, driven by a narrow Store + Driver
// interface, so a slow worker can never stall an API request or the browser.
package interfacereconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/interfacehandoff"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/google/uuid"
)

type Store interface {
	ClaimCoordinatedInterfaceTransitions(ctx context.Context, owner string, limit int, lease time.Duration) ([]postgres.CoordinatedInterfaceTransition, error)
	RenewCoordinatedInterfaceClaim(ctx context.Context, owner, transitionID string, lease time.Duration) error
	AdvanceCoordinatedInterfaceTransition(ctx context.Context, owner, transitionID string, from, to domain.SessionInterfaceTransitionPhase, nativeConversationID, errorCode, errorDetail string, releaseHeldMessages bool) error
	CommitCoordinatedSessionInterface(ctx context.Context, owner, orgID, transitionID string, interfaceValue domain.SessionInterface) (bool, error)
	RollbackCoordinatedSessionInterface(ctx context.Context, owner, orgID, transitionID string) error
	CompleteCoordinatedInterfaceTransition(ctx context.Context, owner, transitionID string) error
	ReleaseCoordinatedInterfaceClaim(ctx context.Context, owner, transitionID string) error
	FailTerminatedInterfaceTransition(ctx context.Context, owner, transitionID string, from domain.SessionInterfaceTransitionPhase, errorCode, errorDetail string) error
}

// WorkerDriver executes handoff steps on the worker that owns the agent process.
type WorkerDriver interface {
	PreflightTarget(ctx context.Context, transition postgres.CoordinatedInterfaceTransition) error
	InspectSource(ctx context.Context, transition postgres.CoordinatedInterfaceTransition) (SourceInspection, error)
	InterruptSource(ctx context.Context, transition postgres.CoordinatedInterfaceTransition) error
	StopSource(ctx context.Context, transition postgres.CoordinatedInterfaceTransition) error
	StopFailedTarget(ctx context.Context, transition postgres.CoordinatedInterfaceTransition) error
	ResolveNativeConversationID(ctx context.Context, transition postgres.CoordinatedInterfaceTransition) (string, error)
	StartTarget(ctx context.Context, transition postgres.CoordinatedInterfaceTransition, nativeConversationID string) error
	// VerifyControllerReady proves that the committed controller has restarted
	// after a recovery-required handoff. Held prompts remain fenced until this
	// proof succeeds.
	VerifyControllerReady(ctx context.Context, transition postgres.CoordinatedInterfaceTransition) error
}

type SourceInspection struct {
	Idle                 bool `json:"idle"`
	WaitingForInput      bool `json:"waitingForInput"`
	DecisionPending      bool `json:"decisionPending"`
	DraftPresent         bool `json:"draftPresent"`
	QuiescenceUnverified bool `json:"quiescenceUnverified"`
}

type Options struct {
	Interval time.Duration
	// StepTimeout bounds one phase advance. It must exceed a worker's slowest
	// command so a healthy handoff is never abandoned mid-flight.
	StepTimeout   time.Duration
	MaxConcurrent int
	// MaxPendingRetries bounds how many ticks a pending worker command may be
	// retried before the handoff fails. A worker that never completes a command
	// is assumed to be unreachable rather than looping forever.
	MaxPendingRetries int
	// UnverifiedIdleLimit bounds a drain whose source cannot prove it is idle
	// (a terminal agent that received input after its last turn ended) while
	// the provider reports no turn in progress. Without it such a drain waits
	// forever. Active work and pending decisions still wait without a limit.
	UnverifiedIdleLimit time.Duration
	// RecoveryRetryBase and RecoveryRetryMax bound how often a fenced
	// recovery_required handoff asks its worker to prove a controller is live.
	// Each failed attempt doubles the wait, from the base up to the max, so a
	// session whose worker is gone (paused, or never coming back) is not asked
	// on every tick. The max stays short: the handoff holds the session's
	// prompts, and a worker that comes back with the same epoch (a resumed VM)
	// waits out at most one interval.
	RecoveryRetryBase time.Duration
	RecoveryRetryMax  time.Duration
	Logger            *slog.Logger
}

const (
	defaultInterval      = 2 * time.Second
	defaultStepTimeout   = 45 * time.Second
	defaultMaxConcurrent = 4
	defaultLease         = 30 * time.Second
	defaultMaxRetries    = 10
	// Long enough for a submitted prompt's provider hook to reach the control
	// plane, so a turn that is just starting is never mistaken for idle.
	defaultUnverifiedIdleLimit = 30 * time.Second
	defaultRecoveryRetryBase   = 2 * time.Second
	defaultRecoveryRetryMax    = 30 * time.Second
)

// SessionTerminatedCode fails a handoff whose session was deleted before it
// finished: no worker will run its remaining steps.
const SessionTerminatedCode = "SESSION_TERMINATED"

// unverifiedDrainDetail starts every failure detail for a drain that could not
// prove its source idle. The desktop recognizes this prefix.
const unverifiedDrainDetail = "source controller activity cannot be verified;"

type Coordinator struct {
	store   Store
	driver  WorkerDriver
	options Options
	owner   string
	lease   time.Duration
	log     *slog.Logger
	retries map[string]int
	// unverifiedSince records when this coordinator first saw a draining source
	// that could not prove it is idle while its provider reported no turn.
	unverifiedSince map[string]time.Time
	// recoveryRetries holds the backoff for recovery_required handoffs whose
	// last readiness or restore attempt failed.
	recoveryRetries map[string]recoveryRetry
	now             func() time.Time
}

// recoveryRetry is when a failed recovery may next be attempted, the wait
// after the attempt that failed, and the session's live worker epoch then.
type recoveryRetry struct {
	next  time.Time
	delay time.Duration
	epoch int64
}

// New creates an interface handoff coordinator.
func New(store Store, driver WorkerDriver, options Options) *Coordinator {
	if options.Interval <= 0 {
		options.Interval = defaultInterval
	}
	if options.StepTimeout <= 0 {
		options.StepTimeout = defaultStepTimeout
	}
	if options.MaxConcurrent <= 0 {
		options.MaxConcurrent = defaultMaxConcurrent
	}
	if options.MaxPendingRetries <= 0 {
		options.MaxPendingRetries = defaultMaxRetries
	}
	if options.UnverifiedIdleLimit <= 0 {
		options.UnverifiedIdleLimit = defaultUnverifiedIdleLimit
	}
	if options.RecoveryRetryBase <= 0 {
		options.RecoveryRetryBase = defaultRecoveryRetryBase
	}
	if options.RecoveryRetryMax < options.RecoveryRetryBase {
		options.RecoveryRetryMax = max(defaultRecoveryRetryMax, options.RecoveryRetryBase)
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Coordinator{
		store:           store,
		driver:          driver,
		options:         options,
		owner:           uuid.NewString(),
		lease:           defaultLease,
		log:             options.Logger,
		retries:         make(map[string]int),
		unverifiedSince: make(map[string]time.Time),
		recoveryRetries: make(map[string]recoveryRetry),
		now:             time.Now,
	}
}

// Run converges interface handoffs until ctx is canceled.
func (c *Coordinator) Run(ctx context.Context) error {
	if err := c.ReconcileOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		c.log.Error("initial interface reconciliation failed", "err", err)
	}
	ticker := time.NewTicker(c.options.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.ReconcileOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				c.log.Error("interface reconciliation failed", "err", err)
			}
		}
	}
}

// ReconcileOnce performs a single pass of pending handoffs.
func (c *Coordinator) ReconcileOnce(ctx context.Context) error {
	seen := make(map[string]struct{}, c.options.MaxConcurrent)
	c.pruneRecoveryRetries(c.now())
	// A recovery waiting out its backoff is released untouched and does not
	// count against MaxConcurrent, so it never delays another session's
	// handoff; the seen set still ends the pass once every claimable
	// transition has had its turn.
	processed := 0
	for processed < c.options.MaxConcurrent {
		// Claim only when ready to process this transition. A claim queued
		// behind another worker operation would expire before renewal starts.
		transitions, err := c.store.ClaimCoordinatedInterfaceTransitions(ctx, c.owner, 1, c.lease)
		if err != nil {
			return err
		}
		if len(transitions) == 0 {
			break
		}
		transition := transitions[0]
		if _, alreadySeen := seen[transition.ID]; alreadySeen {
			return c.store.ReleaseCoordinatedInterfaceClaim(ctx, c.owner, transition.ID)
		}
		seen[transition.ID] = struct{}{}
		if c.waitingToRetryRecovery(transition) {
			if err := c.store.ReleaseCoordinatedInterfaceClaim(ctx, c.owner, transition.ID); err != nil {
				return err
			}
			continue
		}
		processed++
		if err := c.reconcile(ctx, &transition); err != nil {
			if errors.Is(err, errCoordinationLost) {
				c.log.Warn("interface transition claim lost to another coordinator",
					"transition_id", transition.ID, "session_id", transition.SessionID)
				continue
			}
			c.log.Warn("interface transition failed",
				"transition_id", transition.ID, "session_id", transition.SessionID, "err", err)
		}
	}
	return nil
}

var errCoordinationLost = errors.New("interface transition coordination lost")

// errPendingWorkerCommand means a worker command is still in flight. It is not
// a failure: the coordinator releases the claim and re-claims on a later tick,
// resuming from the durable phase row. No terminal phase is written.
var errPendingWorkerCommand = errors.New("interface transition worker command pending")

// A busy drain keeps its durable phase and gives other sessions a turn.
var errSourceBusy = errors.New("source controller is still active")

func (c *Coordinator) reconcile(ctx context.Context, transition *postgres.CoordinatedInterfaceTransition) (result error) {
	runCtx, cancel := context.WithCancelCause(ctx)

	// Renew the claim while a bounded worker operation is in flight. A lost
	// claim aborts the handoff rather than risking two writers.
	renewed := make(chan error, 1)
	go func() {
		interval := c.lease / 3
		if interval < time.Millisecond {
			interval = time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				renewed <- nil
				return
			case <-ticker.C:
				err := c.store.RenewCoordinatedInterfaceClaim(runCtx, c.owner, transition.ID, c.lease)
				if err != nil {
					cancel(errCoordinationLost)
					renewed <- err
					return
				}
			}
		}
	}()
	defer func() {
		cancel(nil)
		if err := <-renewed; err != nil {
			result = fmt.Errorf("%w: renew claim: %v", errCoordinationLost, err)
		}
		_ = c.store.ReleaseCoordinatedInterfaceClaim(ctx, c.owner, transition.ID)
	}()

	// Every durable phase marks the *next* operation to perform. On a retry or
	// coordinator restart we therefore resume at that phase instead of replaying
	// earlier worker commands. The transition only advances after the preceding
	// operation succeeds, so source stopping, native-id resolution, committing,
	// and target start are never repeated once their following checkpoint is
	// durable.
	if transition.SessionTerminated {
		return c.endTerminated(runCtx, *transition)
	}
	for {
		if errors.Is(context.Cause(runCtx), errCoordinationLost) {
			return errCoordinationLost
		}
		switch transition.Phase {
		case domain.SessionInterfaceTransitionRequested:
			if err := c.advance(runCtx, transition, domain.SessionInterfaceTransitionPreflighting, "", ""); err != nil {
				return err
			}
		case domain.SessionInterfaceTransitionPreflighting:
			if err := c.driver.PreflightTarget(runCtx, *transition); err != nil {
				return c.retryOrFail(runCtx, *transition, "TARGET_PREFLIGHT_FAILED", err, true)
			}
			if err := c.advance(runCtx, transition, domain.SessionInterfaceTransitionDraining, "", ""); err != nil {
				return err
			}
		case domain.SessionInterfaceTransitionDraining:
			if err, sourceUsable := c.drain(runCtx, *transition); err != nil {
				if errors.Is(err, errSourceBusy) {
					return nil
				}
				return c.retryOrFail(runCtx, *transition, "SOURCE_DRAIN_FAILED", err, sourceUsable)
			}
			if err := c.advance(runCtx, transition, domain.SessionInterfaceTransitionSourceStopping, "", ""); err != nil {
				return err
			}
		case domain.SessionInterfaceTransitionSourceStopping:
			if err := c.driver.StopSource(runCtx, *transition); err != nil {
				return c.retryOrFail(runCtx, *transition, "SOURCE_STOP_FAILED", err, false)
			}
			if err := c.advance(runCtx, transition, domain.SessionInterfaceTransitionSourceStopped, "", ""); err != nil {
				return err
			}
		case domain.SessionInterfaceTransitionSourceStopped:
			nativeID, err := c.driver.ResolveNativeConversationID(runCtx, *transition)
			if err != nil {
				return c.retryOrFail(runCtx, *transition, "NATIVE_ID_RESOLUTION_FAILED", err, false)
			}
			committed, err := c.store.CommitCoordinatedSessionInterface(
				runCtx, c.owner, transition.OrgID, transition.ID, transition.TargetInterface,
			)
			if err != nil {
				if errors.Is(err, postgres.ErrTransitionStale) {
					return errCoordinationLost
				}
				return c.fail(runCtx, *transition, "SESSION_COMMIT_FAILED", err, false)
			}
			if !committed {
				return c.fail(runCtx, *transition, "SESSION_NOT_FOUND", errors.New("session changed before interface commit"), false)
			}
			if err := c.advance(runCtx, transition, domain.SessionInterfaceTransitionTargetStarting, nativeID, ""); err != nil {
				return err
			}
		case domain.SessionInterfaceTransitionTargetStarting:
			if err := c.driver.StartTarget(runCtx, *transition, transition.NativeConversationID); err != nil {
				if !isRetryable(err) {
					// The session is committed to the new interface. The target failed to
					// start, so mark recovery instead of failing: a user must never be left
					// with no controller.
					return c.recover(runCtx, *transition, "TARGET_START_FAILED", err)
				}
				return c.retryOrFail(runCtx, *transition, "TARGET_START_FAILED", err, false)
			}
			if err := c.advance(runCtx, transition, domain.SessionInterfaceTransitionActivating, transition.NativeConversationID, ""); err != nil {
				return err
			}
		case domain.SessionInterfaceTransitionActivating:
			if err := c.store.CompleteCoordinatedInterfaceTransition(runCtx, c.owner, transition.ID); err != nil {
				if errors.Is(err, postgres.ErrTransitionStale) {
					return errCoordinationLost
				}
				return fmt.Errorf("complete interface transition: %w", err)
			}
			transition.Phase = domain.SessionInterfaceTransitionCompleted
			return nil
		case domain.SessionInterfaceTransitionRecovery:
			return c.retryRecoveryLater(*transition, c.recoverController(runCtx, transition))
		default:
			return nil
		}
	}
}

// recoverController runs one recovery attempt for a recovery_required handoff.
func (c *Coordinator) recoverController(ctx context.Context, transition *postgres.CoordinatedInterfaceTransition) error {
	if transition.ErrorCode == "TARGET_START_FAILED" {
		return c.restoreSource(ctx, *transition)
	}
	if uncommittedRecovery(transition.ErrorCode) {
		return c.restoreUncommittedSource(ctx, *transition)
	}
	// Recovery is terminal until a replacement worker proves that the
	// committed controller is live. Only then may the store atomically
	// release prompts held by this transition.
	if err := c.driver.VerifyControllerReady(ctx, *transition); err != nil {
		return err
	}
	if err := c.store.CompleteCoordinatedInterfaceTransition(ctx, c.owner, transition.ID); err != nil {
		if errors.Is(err, postgres.ErrTransitionStale) {
			return errCoordinationLost
		}
		return fmt.Errorf("complete recovered interface transition: %w", err)
	}
	transition.Phase = domain.SessionInterfaceTransitionCompleted
	return nil
}

// uncommittedRecovery reports a recovery whose failure came before the session
// row was committed to the target interface, so the controller to restore and
// verify is the source. A drain is one: the interrupt policy recovers a failed
// source interrupt (the source may be stopped) before anything is committed.
func uncommittedRecovery(errorCode string) bool {
	switch errorCode {
	case "SOURCE_DRAIN_FAILED", "SOURCE_STOP_FAILED", "NATIVE_ID_RESOLUTION_FAILED",
		"SESSION_COMMIT_FAILED", "SESSION_NOT_FOUND":
		return true
	default:
		return false
	}
}

// retryRecoveryLater records the backoff after a failed recovery attempt (or
// clears it after one that finished) and returns err with when the next
// attempt is due. A lost claim is not the recovery's failure, so it is
// returned as is and does not back off.
func (c *Coordinator) retryRecoveryLater(transition postgres.CoordinatedInterfaceTransition, err error) error {
	if err == nil {
		delete(c.recoveryRetries, transition.ID)
		return nil
	}
	if errors.Is(err, errCoordinationLost) {
		return err
	}
	if errors.Is(err, errPendingWorkerCommand) {
		// A worker answering slowly is there: try again on the next tick.
		delete(c.recoveryRetries, transition.ID)
		return err
	}
	delay := c.options.RecoveryRetryBase
	if previous, ok := c.recoveryRetries[transition.ID]; ok && previous.epoch == transition.LiveWorkerEpoch {
		delay = min(previous.delay*2, c.options.RecoveryRetryMax)
	}
	c.recoveryRetries[transition.ID] = recoveryRetry{next: c.now().Add(delay), delay: delay, epoch: transition.LiveWorkerEpoch}
	return fmt.Errorf("recover controller (next attempt in %s): %w", delay, err)
}

// waitingToRetryRecovery reports a recovery whose next attempt is not due yet.
// A session whose live worker epoch changed since the failed attempt (a new
// worker connected, or the last one went away) is tried at once, starting its
// backoff over.
func (c *Coordinator) waitingToRetryRecovery(transition postgres.CoordinatedInterfaceTransition) bool {
	if transition.Phase != domain.SessionInterfaceTransitionRecovery || transition.SessionTerminated {
		delete(c.recoveryRetries, transition.ID)
		return false
	}
	retry, ok := c.recoveryRetries[transition.ID]
	if ok && retry.epoch != transition.LiveWorkerEpoch {
		delete(c.recoveryRetries, transition.ID)
		return false
	}
	return ok && c.now().Before(retry.next)
}

// pruneRecoveryRetries drops backoff for transitions this coordinator has not
// attempted for a while (finished elsewhere, or claimed by another replica),
// so the map cannot grow forever.
func (c *Coordinator) pruneRecoveryRetries(now time.Time) {
	for id, retry := range c.recoveryRetries {
		if now.Sub(retry.next) > 2*c.options.RecoveryRetryMax {
			delete(c.recoveryRetries, id)
		}
	}
}

// endTerminated fails a handoff whose session was deleted: its worker is gone
// and a deleted session never gets another, so no step (and no recovery proof)
// can run. Without this a recovery_required handoff on a deleted session asks
// for a worker on every tick forever. Prompts it held become failed turns
// that say they were not sent because the session was deleted
// (postgres.TerminatedInterfaceMessageError): never lost silently, and never
// run unasked once the session is restored.
func (c *Coordinator) endTerminated(ctx context.Context, transition postgres.CoordinatedInterfaceTransition) error {
	delete(c.recoveryRetries, transition.ID)
	delete(c.unverifiedSince, transition.ID)
	delete(c.retries, transition.ID)
	detail := "The session was deleted before the interface switch finished, so the switch was abandoned."
	if transition.ErrorDetail != "" {
		detail += " Earlier failure (" + transition.ErrorCode + "): " + transition.ErrorDetail
	}
	stepCtx, cancel := context.WithTimeout(ctx, c.options.StepTimeout)
	defer cancel()
	err := c.store.FailTerminatedInterfaceTransition(stepCtx, c.owner, transition.ID, transition.Phase,
		SessionTerminatedCode, detail)
	if errors.Is(err, postgres.ErrTransitionStale) {
		return errCoordinationLost
	}
	if err != nil {
		return fmt.Errorf("end interface transition of a deleted session: %w", err)
	}
	c.log.Info("interface transition ended: session deleted",
		"transition_id", transition.ID, "session_id", transition.SessionID,
		"phase", transition.Phase, "previous_error_code", transition.ErrorCode)
	return nil
}

// Before the session interface is committed, only the source may have run.
// A timed-out stop leaves its state uncertain, so restart and verify that
// source before ending the failed handoff and releasing held prompts.
func (c *Coordinator) restoreUncommittedSource(ctx context.Context, transition postgres.CoordinatedInterfaceTransition) error {
	source := transition
	source.TargetInterface = transition.SourceInterface
	if err := c.driver.VerifyControllerReady(ctx, source); err != nil {
		if err := c.driver.StartTarget(ctx, source, transition.NativeConversationID); err != nil {
			return fmt.Errorf("restart uncommitted source controller: %w", err)
		}
		if err := c.driver.VerifyControllerReady(ctx, source); err != nil {
			return fmt.Errorf("verify uncommitted source controller: %w", err)
		}
	}
	err := c.store.AdvanceCoordinatedInterfaceTransition(
		ctx, c.owner, transition.ID, domain.SessionInterfaceTransitionRecovery,
		domain.SessionInterfaceTransitionFailed, transition.NativeConversationID,
		transition.ErrorCode, transition.ErrorDetail, true,
	)
	if errors.Is(err, postgres.ErrTransitionStale) {
		return errCoordinationLost
	}
	return err
}

// restoreSource follows the local handoff's rollback rule. Each operation is
// idempotent across coordinator restarts: stop only the failed target, restore
// the durable source mode, then start and verify that source before releasing
// held messages. An uncertain stop or restart leaves recovery fenced.
func (c *Coordinator) restoreSource(ctx context.Context, transition postgres.CoordinatedInterfaceTransition) error {
	if err := c.driver.StopFailedTarget(ctx, transition); err != nil {
		return fmt.Errorf("stop failed target before rollback: %w", err)
	}
	if err := c.store.RollbackCoordinatedSessionInterface(ctx, c.owner, transition.OrgID, transition.ID); err != nil {
		return fmt.Errorf("restore committed source interface: %w", err)
	}
	source := transition
	source.TargetInterface = transition.SourceInterface
	if err := c.driver.StartTarget(ctx, source, transition.NativeConversationID); err != nil {
		return fmt.Errorf("restart source controller: %w", err)
	}
	if err := c.driver.VerifyControllerReady(ctx, source); err != nil {
		return fmt.Errorf("verify restored source controller: %w", err)
	}
	// Recovery is already a terminal checkpoint in the normal state table.
	// After proving the original controller is live, close this specific
	// recovery attempt as failed and release held messages atomically.
	err := c.store.AdvanceCoordinatedInterfaceTransition(
		ctx, c.owner, transition.ID, domain.SessionInterfaceTransitionRecovery,
		domain.SessionInterfaceTransitionFailed, transition.NativeConversationID,
		"TARGET_START_FAILED", transition.ErrorDetail, true,
	)
	if errors.Is(err, postgres.ErrTransitionStale) {
		return errCoordinationLost
	}
	return err
}

// retryOrFail releases a pending retryable worker command, or fails the
// transition once it has been retried too many times. Target-start failures
// already committed to the target interface recover instead of failing.
func (c *Coordinator) retryOrFail(
	ctx context.Context,
	transition postgres.CoordinatedInterfaceTransition,
	errorCode string,
	err error,
	sourceUsable bool,
) error {
	if errors.Is(context.Cause(ctx), errCoordinationLost) {
		return errCoordinationLost
	}
	if !isRetryable(err) {
		return c.fail(ctx, transition, errorCode, err, sourceUsable)
	}
	c.retries[transition.ID]++
	if c.retries[transition.ID] <= c.options.MaxPendingRetries {
		c.log.Warn("interface transition retrying pending worker command",
			"transition_id", transition.ID,
			"session_id", transition.SessionID,
			"attempt", c.retries[transition.ID],
		)
		return err
	}
	delete(c.retries, transition.ID)
	// Once source stopping has begun, a remote command failure cannot prove
	// which controller is alive. The shared state table therefore requires
	// recovery rather than releasing held prompts from a possibly dead source.
	if interfacehandoff.FailureOutcome(transition.Phase) == domain.SessionInterfaceTransitionRecovery {
		return c.recover(ctx, transition, errorCode, fmt.Errorf(
			"worker never completed the interface command after %d attempts: %w",
			c.options.MaxPendingRetries, err,
		))
	}
	return c.fail(ctx, transition, errorCode, fmt.Errorf(
		"worker never completed the interface command after %d attempts: %w",
		c.options.MaxPendingRetries, err,
	), sourceUsable)
}

// isRetryable reports an error that must release the claim and retry on a later
// tick rather than fail the interface transition. Pending worker commands and
// lost coordination both fall through to a clean re-claim from the durable row.
func isRetryable(err error) bool {
	return errors.Is(err, errPendingWorkerCommand) || errors.Is(err, errCoordinationLost)
}

// drain checks whether the source controller is quiescent. A busy source is
// retried on the next pass so it cannot hold up another session's handoff.
// An interrupt policy cancels in-flight work first.
func (c *Coordinator) drain(ctx context.Context, transition postgres.CoordinatedInterfaceTransition) (error, bool) {
	if transition.Policy == domain.SessionInterfaceTransitionInterrupt {
		// Stop-now is the explicit opt-in to ending a source that cannot prove it
		// is idle (notably an interactive TUI). The worker stop step waits for the
		// controller process to exit before starting the target.
		return c.driver.InterruptSource(ctx, transition), false
	}
	inspection, err := c.driver.InspectSource(ctx, transition)
	if err != nil {
		return err, false
	}
	now := c.now()
	c.pruneUnverified(now)
	if inspection.QuiescenceUnverified && !inspection.DecisionPending && !inspection.DraftPresent &&
		providerReportsNoTurn(transition) {
		// A terminal proves it is idle only by a Stop hook newer than its latest
		// input. Input that never starts a turn (a message a dialog swallowed,
		// an unsent draft, a scroll or protocol report) leaves that proof
		// missing forever while the provider reports no work. Wait a bounded
		// time for a turn to start and finish, then fail with the source left
		// running, as the local daemon does (DRAIN_QUIESCENCE_UNVERIFIED).
		since, seen := c.unverifiedSince[transition.ID]
		if !seen {
			since = now
			c.unverifiedSince[transition.ID] = now
		}
		if now.Sub(since) >= c.options.UnverifiedIdleLimit {
			delete(c.unverifiedSince, transition.ID)
			return fmt.Errorf(
				"%s the agent reported no running turn for %s, but the terminal could not prove it is idle "+
					"since its latest input. The terminal was left running; switch with the interrupt policy to stop it now",
				unverifiedDrainDetail, c.options.UnverifiedIdleLimit,
			), true
		}
		return errSourceBusy, true
	}
	delete(c.unverifiedSince, transition.ID)
	if inspection.DecisionPending || inspection.DraftPresent || inspection.QuiescenceUnverified {
		return errSourceBusy, true
	}
	if inspection.Idle {
		return nil, true
	}
	return errSourceBusy, true
}

// providerReportsNoTurn reports that the session's provider hooks show no turn
// in progress: the session is idle, or it is "active" only because AO typed a
// message that no provider hook has confirmed as a submitted prompt.
func providerReportsNoTurn(transition postgres.CoordinatedInterfaceTransition) bool {
	switch transition.SessionActivityState {
	case "idle":
		return true
	case "active":
		return transition.SessionActivityUnconfirmed
	default:
		return false
	}
}

// pruneUnverified drops clocks for transitions that left draining elsewhere
// (cancelled, or claimed by another replica) so the map cannot grow forever.
func (c *Coordinator) pruneUnverified(now time.Time) {
	for id, since := range c.unverifiedSince {
		if now.Sub(since) > 10*c.options.UnverifiedIdleLimit {
			delete(c.unverifiedSince, id)
		}
	}
}

func (c *Coordinator) advance(
	ctx context.Context,
	transition *postgres.CoordinatedInterfaceTransition,
	to domain.SessionInterfaceTransitionPhase,
	nativeID, detail string,
) error {
	if !interfacehandoff.CanAdvance(transition.Phase, to) {
		return fmt.Errorf("invalid interface transition phase edge %q -> %q", transition.Phase, to)
	}
	stepCtx, cancel := context.WithTimeout(ctx, c.options.StepTimeout)
	defer cancel()
	err := c.store.AdvanceCoordinatedInterfaceTransition(
		stepCtx,
		c.owner,
		transition.ID,
		transition.Phase,
		to,
		nativeID,
		"",
		detail,
		false,
	)
	if errors.Is(err, postgres.ErrTransitionStale) {
		return errCoordinationLost
	}
	if err != nil {
		return fmt.Errorf("advance interface transition to %s: %w", to, err)
	}
	transition.Phase = to
	transition.NativeConversationID = nativeID
	return nil
}

func (c *Coordinator) fail(
	parentCtx context.Context,
	transition postgres.CoordinatedInterfaceTransition,
	errorCode string,
	cause error,
	sourceUsable bool,
) error {
	if errors.Is(context.Cause(parentCtx), errCoordinationLost) {
		return errCoordinationLost
	}
	// A failure while the source is still usable can safely release prompts in
	// the same transaction as the terminal failed outcome. If the worker did
	// not prove that, keep the handoff fenced for recovery rather than exposing
	// accepted prompts to a controller that may no longer exist.
	if interfacehandoff.FailureOutcome(transition.Phase) == domain.SessionInterfaceTransitionRecovery ||
		(!sourceUsable && transition.Phase != domain.SessionInterfaceTransitionPreflighting) {
		return c.recover(parentCtx, transition, errorCode, cause)
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.options.StepTimeout)
	defer cancel()
	err := c.store.AdvanceCoordinatedInterfaceTransition(
		ctx, c.owner, transition.ID, transition.Phase,
		domain.SessionInterfaceTransitionFailed, transition.NativeConversationID,
		errorCode, cause.Error(),
		sourceUsable,
	)
	if errors.Is(err, postgres.ErrTransitionStale) {
		return errCoordinationLost
	}
	return err
}

// recover marks a committed-interface target failure as recovery_required so a
// session is never left with no controller. It runs under a fresh context so a
// drained or canceled request context cannot skip the durable write.
func (c *Coordinator) recover(
	parentCtx context.Context,
	transition postgres.CoordinatedInterfaceTransition,
	errorCode string,
	cause error,
) error {
	if errors.Is(context.Cause(parentCtx), errCoordinationLost) {
		return errCoordinationLost
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.options.StepTimeout)
	defer cancel()
	err := c.store.AdvanceCoordinatedInterfaceTransition(
		ctx, c.owner, transition.ID, transition.Phase,
		domain.SessionInterfaceTransitionRecovery, transition.NativeConversationID,
		errorCode, cause.Error(),
		false,
	)
	if errors.Is(err, postgres.ErrTransitionStale) {
		return errCoordinationLost
	}
	return err
}
