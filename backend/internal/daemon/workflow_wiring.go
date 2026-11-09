package daemon

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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

const workflowLeaseDuration = 15 * time.Minute

// newWorkflowService builds the Workflow service from the durable store. Clock,
// ID, and lease-token sources stay nil so the service applies its own defaults.
func newWorkflowService(store *sqlite.Store) *workflowsvc.Service {
	return newWorkflowServiceWithAdmission(store, nil)
}

// newWorkflowServiceWithAdmission 让 HTTP 与自动 Claim 共用同一原子准入边界。
func newWorkflowServiceWithAdmission(store *sqlite.Store, admission ports.WorkflowAdmissionProvider) *workflowsvc.Service {
	return workflowsvc.New(workflowsvc.Deps{
		Store:     store,
		Admission: admission,
		GraphLimits: domain.WorkflowGraphLimits{
			MaxTasks: workflowMaxTasks, MaxDepth: workflowMaxDepth, MaxFanOut: workflowMaxFanOut,
		},
		LeaseDuration: workflowLeaseDuration,
	})
}
