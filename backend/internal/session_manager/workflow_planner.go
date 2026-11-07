package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// workflowPlanner 从持久 Task 决定沙箱，启动与恢复都不能用提示词或 Renderer 字段解除只读。
func (m *Manager) workflowPlanner(ctx context.Context, identity *ports.WorkflowSpawnContext) (bool, error) {
	if identity == nil {
		return false, nil
	}
	reader, ok := m.store.(interface {
		GetWorkflowTask(context.Context, domain.WorkflowID, domain.WorkflowTaskID) (domain.WorkflowTask, bool, error)
	})
	if !ok {
		return false, fmt.Errorf("workflow planner identity unavailable")
	}
	task, found, err := reader.GetWorkflowTask(ctx, identity.WorkflowID, identity.TaskID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("workflow task identity missing")
	}
	if task.Planner && task.AgentHarness != domain.HarnessCodex {
		return false, fmt.Errorf("workflow planner read-only sandbox unsupported")
	}
	return task.Planner, nil
}
