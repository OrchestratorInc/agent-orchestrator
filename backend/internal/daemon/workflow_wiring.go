package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	workflowsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// Workflow graph bounds and lease settings are explicit M1 deployment defaults.
// The design document's 3/10 concurrent-worker ceiling is an M3 scheduler
// concern and is deliberately not expressed here: these numbers only bound a
// single workflow's DAG. The lease must comfortably exceed one worker's
// expected step time, because an expired lease cannot be resumed.
const (
	workflowMaxTasks  = 50
	workflowMaxDepth  = 12
	workflowMaxFanOut = 8
)

const (
	workflowLeaseDuration      = 15 * time.Minute
	workflowLeaseSweepInterval = time.Minute
)

// newWorkflowService builds the Workflow service from the durable store. Clock,
// ID, and lease-token sources stay nil so the service applies its own defaults.
func newWorkflowService(store *sqlite.Store) *workflowsvc.Service {
	return workflowsvc.New(workflowsvc.Deps{
		Store: store,
		GraphLimits: domain.WorkflowGraphLimits{
			MaxTasks: workflowMaxTasks, MaxDepth: workflowMaxDepth, MaxFanOut: workflowMaxFanOut,
		},
		LeaseDuration: workflowLeaseDuration,
	})
}

// startWorkflowLeaseRecovery reconciles expired workflow leases: once at
// startup, then every workflowLeaseSweepInterval until ctx is cancelled. The
// first sweep runs synchronously so a restart reconciles before any scheduler
// work can see the tasks.
//
// Recovery is fail-closed. An expired lease means the outcome of the worker's
// last action is unknown, so recovery marks the attempt UNKNOWN and blocks its
// task instead of re-dispatching it. Without this loop a restart leaves a
// CLAIMED attempt claimed forever: Claim requires READY and Start requires an
// unexpired lease, so the task can never be scheduled again.
func startWorkflowLeaseRecovery(ctx context.Context, svc *workflowsvc.Service, log *slog.Logger) {
	if svc == nil {
		return
	}
	sweep := func() {
		count, err := svc.RecoverExpired(ctx)
		switch {
		case err != nil:
			log.Warn("workflow lease recovery failed", "err", err)
		case count > 0:
			log.Warn("workflow leases expired", "count", count)
		}
	}
	sweep()
	go func() {
		defer sentryobs.RecoverBackground()
		ticker := time.NewTicker(workflowLeaseSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()
}
