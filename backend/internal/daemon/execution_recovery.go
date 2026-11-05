package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// M2 binding recovery.
//
// The coordinator's own rule is that an unknown spawn is never resolved by spawning
// again: it is resolved by asking the owner whether a session exists for the stable
// spawn request id. This sweep is the periodic form of that question, so a daemon
// restart does not leave a binding frozen in spawn_unknown until a human notices.
//
// Three properties make it safe to run forever:
//
//   - it never spawns. Reconcile only looks up; a binding with no session stays
//     frozen, which is the correct outcome and not a failure to retry;
//   - it is bounded in both work and time per sweep, so a large backlog cannot turn
//     startup into an unbounded provider session;
//   - one workflow's failure never aborts the sweep, because a single unreadable
//     binding must not stop every other workflow's recovery.

const (
	// m2BindingRecoveryInterval is how often a started daemon re-checks frozen
	// bindings. Recovery is not latency-sensitive: a binding waiting here has already
	// been waiting for a restart.
	m2BindingRecoveryInterval = 1 * time.Minute
	// m2BindingRecoveryBatch bounds one sweep so the work is proportional to the
	// interval rather than to the size of the backlog.
	m2BindingRecoveryBatch = 50
	// m2BindingRecoveryTimeout bounds one whole sweep, not one binding: a hung store
	// read must not keep the loop from noticing cancellation.
	m2BindingRecoveryTimeout = 20 * time.Second
)

// m2ReconcilableStates are the binding states the coordinator can actually advance by
// looking up an existing session.
//
// recovery_uncertain is deliberately absent. The coordinator documents it as a state
// only new evidence may settle, and Reconcile explicitly declines it, so sweeping it
// would consume the batch on rows nothing can move and starve the rows that can be
// resolved. It is counted separately instead (see m2FrozenStates).
var m2ReconcilableStates = []domain.WorkflowBindingState{
	domain.WorkflowBindingStateSpawnRequested,
	domain.WorkflowBindingStateSpawnUnknown,
	domain.WorkflowBindingStateReconcileRequired,
}

// bindingReconciler is the coordinator surface the sweep uses. It is one method so a
// test can prove the sweep calls it without standing up a coordinator.
type bindingReconciler interface {
	Reconcile(ctx context.Context, attemptID domain.WorkflowAttemptID) (execution.BindOutcome, error)
}

// bindingStateLister is the store read the sweep needs.
type bindingStateLister interface {
	ListWorkflowAttemptBindingsByState(
		ctx context.Context,
		states []domain.WorkflowBindingState,
		limit int64,
	) ([]domain.WorkflowAttemptBinding, error)
}

// recoverySweepResult is what one sweep observed, as counts only: the log line this
// feeds must never carry an attempt id, a workflow id, or a session id.
type recoverySweepResult struct {
	// Examined is how many reconcilable bindings the sweep listed.
	Examined int
	// Advanced is how many moved to a different state.
	Advanced int
	// Frozen is how many are still frozen after reconciliation (no session found).
	Frozen int
	// Failed is how many reconciliations returned an error.
	Failed int
	// Uncertain is how many bindings sit in a state only new evidence can settle. It is
	// counted from the reconcile outcome, not from a second query, so a listing failure
	// cannot misreport the backlog.
	Uncertain int
	// Truncated reports that the batch limit was hit, so more work remains.
	Truncated bool
}

// bindingRecoverySweep is the sweep's logic, separated from its ticker so tests can run
// one sweep deterministically.
type bindingRecoverySweep struct {
	binder bindingReconciler
	store  bindingStateLister
	batch  int64
	log    *slog.Logger
}

// run performs one bounded sweep.
func (s bindingRecoverySweep) run(ctx context.Context) recoverySweepResult {
	var result recoverySweepResult
	if s.binder == nil || s.store == nil {
		return result
	}

	bindings, err := s.store.ListWorkflowAttemptBindingsByState(ctx, m2ReconcilableStates, s.batch)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("M2 binding recovery could not list bindings", "reason", "list_failed")
		}
		return result
	}
	result.Examined = len(bindings)
	result.Truncated = int64(len(bindings)) == s.batch

	for _, binding := range bindings {
		if ctx.Err() != nil {
			return result
		}
		previous := binding.State
		outcome, reconcileErr := s.binder.Reconcile(ctx, binding.AttemptID)
		switch {
		case reconcileErr != nil && isSettledNoOp(reconcileErr):
			// The binding is already at the outcome this sweep would record.
			//
			// This is the normal shape of reconcile_required with no session: the
			// coordinator's own answer for "nothing to adopt" IS reconcile_required, so
			// asking again produces a same-state transition the domain rightly rejects.
			// Counting that as a failure would log an error every minute forever for a
			// row that is working as designed, and a real failure would disappear into
			// the noise.
			result.Frozen++
		case reconcileErr != nil:
			// One workflow's failure must not abort the sweep: the rest may still be
			// resolvable, and the failure is retried on the next pass.
			result.Failed++
			if ctx.Err() == nil {
				s.log.Warn("M2 binding recovery failed for one binding",
					"state", string(previous), "reason", "reconcile_failed", "err", reconcileErr)
			}
			continue
		case outcome.Binding.State != previous:
			result.Advanced++
		case outcome.Frozen:
			result.Frozen++
		default:
			// Unchanged and not frozen: this is a binding only new evidence can settle
			// (recovery_uncertain, which the coordinator declines to act on).
			result.Uncertain++
		}
	}

	return result
}

// isSettledNoOp reports whether a reconcile error means "already at the target state".
//
// The two sentinels are distinct causes with the same meaning for a sweep:
//
//   - ErrInvalidWorkflowBindingTransition: the coordinator tried to record the state the
//     binding already has;
//   - ErrWorkflowBindingRevisionConflict: another writer moved the binding first, so this
//     sweep's view is stale and the next one will see the newer state.
//
// Neither is a failure of the workflow, and neither may be retried by spawning.
func isSettledNoOp(err error) bool {
	return errors.Is(err, domain.ErrInvalidWorkflowBindingTransition) ||
		errors.Is(err, domain.ErrWorkflowBindingRevisionConflict)
}

// startM2BindingRecovery runs one synchronous sweep at startup and then repeats on a
// ticker until ctx is cancelled.
//
// The first sweep is synchronous on purpose: a restart is exactly the moment the
// unknown outcomes were produced, so the operator should see them resolved before the
// daemon reports itself ready. The loop then runs in the background.
func startM2BindingRecovery(
	ctx context.Context,
	binder *execution.Coordinator,
	store *sqlite.Store,
	log *slog.Logger,
) {
	if binder == nil || store == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	sweep := bindingRecoverySweep{binder: binder, store: store, batch: m2BindingRecoveryBatch, log: log}

	runOnce := func() {
		sweepCtx, cancel := context.WithTimeout(ctx, m2BindingRecoveryTimeout)
		defer cancel()
		result := sweep.run(sweepCtx)
		if result.Examined == 0 && result.Uncertain == 0 {
			return
		}
		log.Info("M2 binding recovery sweep",
			"examined", result.Examined,
			"advanced", result.Advanced,
			"frozen", result.Frozen,
			"failed", result.Failed,
			"uncertain", result.Uncertain,
			"truncated", result.Truncated)
	}

	runOnce()
	go func() {
		defer sentryobs.RecoverBackground()
		ticker := time.NewTicker(m2BindingRecoveryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runOnce()
			}
		}
	}()
}
