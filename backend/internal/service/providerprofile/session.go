package providerprofile

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sessionReader 只读取持久绑定与任务，不依赖活动 Controller 的可丢失缓存。
type sessionReader interface {
	GetWorkflowAttemptBindingBySession(context.Context, domain.SessionID) (domain.WorkflowAttemptBinding, bool, error)
	GetWorkflowTask(context.Context, domain.WorkflowID, domain.WorkflowTaskID) (domain.WorkflowTask, bool, error)
}

// SessionSelection 从持久绑定恢复路由；已知绑定的任务不可读时禁止退回原生配置。
func SessionSelection(ctx context.Context, store any, id domain.SessionID) (ports.ProviderProfileChoice, error) {
	reader, ok := store.(sessionReader)
	if !ok {
		return ports.ProviderProfileChoice{}, nil
	}
	binding, found, err := reader.GetWorkflowAttemptBindingBySession(ctx, id)
	if err != nil || !found {
		return ports.ProviderProfileChoice{}, err
	}
	task, found, err := reader.GetWorkflowTask(ctx, binding.WorkflowID, binding.TaskID)
	if err != nil {
		return ports.ProviderProfileChoice{}, err
	}
	if !found {
		return ports.ProviderProfileChoice{}, errors.New("workflow provider task identity missing")
	}
	return ports.ProviderProfileChoice{Reference: task.Profile, Harness: task.AgentHarness, Model: task.Model, Effort: task.Effort}, nil
}

// RefuseNativeTransition 在修改账户、模式或 Harness 之前阻止固定供应商任务离开受支持路径。
func RefuseNativeTransition(ctx context.Context, store any, id domain.SessionID) error {
	selection, err := SessionSelection(ctx, store, id)
	if err != nil {
		return err
	}
	if selection.Reference != "" {
		return errors.New("固定供应商任务不支持切换原生账户、终端或 Agent，请通过工作流重新规划")
	}
	return nil
}

// ScopeCapabilityRequest 将会话准入绑定持久路由；请求携带的另一引用不能覆盖归属。
func ScopeCapabilityRequest(ctx context.Context, store any, request ports.CapabilityProbeRequest) (ports.CapabilityProbeRequest, error) {
	if !request.SessionScoped() {
		return request, nil
	}
	selection, err := SessionSelection(ctx, store, request.SessionID)
	if err != nil {
		return request, err
	}
	if selection.Harness == "" && request.Profile == "" {
		return request, nil
	}
	if selection.Harness != "" && selection.Harness != request.Harness || request.Profile != "" && request.Profile != selection.Reference {
		return request, errors.New("session provider identity mismatch")
	}
	request.Profile, request.Model, request.Effort = selection.Reference, selection.Model, selection.Effort
	return request, nil
}
