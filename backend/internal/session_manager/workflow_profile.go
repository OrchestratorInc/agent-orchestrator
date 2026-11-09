package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetProviderProfiles 仅在 daemon 装配阶段安装宿主解析器，不提供运行时写配置入口。
func (m *Manager) SetProviderProfiles(profiles ports.ProviderProfiles) { m.profiles = profiles }

// workflowProfile 从持久任务恢复选择，在资源/启动副作用前验证摘要、模型和凭据可用性。
func (m *Manager) workflowProfile(ctx context.Context, identity *ports.WorkflowSpawnContext, harness domain.AgentHarness, model, effort string) (string, error) {
	if identity == nil {
		return "", nil
	}
	reader, ok := m.store.(interface {
		GetWorkflowTask(context.Context, domain.WorkflowID, domain.WorkflowTaskID) (domain.WorkflowTask, bool, error)
	})
	if !ok {
		return "", fmt.Errorf("workflow provider identity unavailable")
	}
	task, found, err := reader.GetWorkflowTask(ctx, identity.WorkflowID, identity.TaskID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("workflow task identity missing")
	}
	if task.Profile == "" {
		return "", nil
	}
	if m.profiles == nil || task.AgentHarness != harness || task.Model != model || task.Effort != effort {
		return "", fmt.Errorf("workflow provider selection mismatch")
	}
	if _, err := m.profiles.Environment(ctx, task.Profile, harness, model, effort); err != nil {
		return "", err
	}
	return task.Profile, nil
}
