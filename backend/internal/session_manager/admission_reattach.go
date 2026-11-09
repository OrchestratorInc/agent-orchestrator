package sessionmanager

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sessionReattachStore 仅发放并验证观察权限；与可能启动 Provider 的恢复准入隔离。
type sessionReattachStore interface {
	PrepareSessionReattach(context.Context, domain.SessionRecord, domain.SchedulerFence, time.Time) (domain.SessionReattachPermit, error)
	ValidateSessionReattach(context.Context, domain.SessionReattachPermit, string, time.Time) error
}

// prepareSessionReattach 只取本实例 CurrentFence，不读取资源/预算，也不把数据库新 Leader 冒认为本实例。
func (m *Manager) prepareSessionReattach(ctx context.Context, rec domain.SessionRecord) (context.Context, error) {
	store, ok := m.store.(sessionReattachStore)
	if !ok {
		return ctx, nil
	}
	provider, ok := m.admission.(interface {
		CurrentFence(context.Context) (domain.SchedulerFence, error)
	})
	if !ok {
		return ctx, domain.ErrAdmissionRequired
	}
	fence, err := provider.CurrentFence(ctx)
	if err != nil {
		return ctx, err
	}
	p, err := store.PrepareSessionReattach(ctx, rec, fence, m.clock().UTC())
	if err != nil {
		return ctx, err
	}
	return ports.WithSessionReattachPermit(ctx, p, m.clock), nil
}

// reattachWorkflowContext 仅从 Store 已核对的归属构造非秘密身份，不把观察许可转换为业务执行许可。
func (m *Manager) reattachWorkflowContext(ctx context.Context, rec domain.SessionRecord) (*ports.WorkflowSpawnContext, error) {
	if p, ok := ports.SessionReattachPermitFromContext(ctx); ok {
		if p.Execution.AttemptID == "" {
			return nil, nil
		}
		return &ports.WorkflowSpawnContext{WorkflowID: p.Execution.WorkflowID, TaskID: p.Execution.TaskID, AttemptID: p.Execution.AttemptID}, nil
	}
	return m.workflowSpawnContext(ctx, rec)
}

// validateReattachedController 拒绝新 Provider 或迟到连接；Chat 所有者负责只关闭本次未发布连接。
func (m *Manager) validateReattachedController(ctx context.Context, started ChatStarted) error {
	p, ok := ports.SessionReattachPermitFromContext(ctx)
	if !ok {
		return nil
	}
	if !started.LiveReconnect || started.ProviderConversationID != p.Owner.ProviderConversationID || started.ControllerGeneration == "" {
		return domain.ErrSchedulerAdmissionConflict
	}
	return m.validateSessionReattach(ctx, p, started.ControllerGeneration)
}

// validateSessionReattach 在成功发布和失败回执两个边界复验许可；缺少事务能力时闭锁。
func (m *Manager) validateSessionReattach(ctx context.Context, permit domain.SessionReattachPermit, generation string) error {
	store, ok := m.store.(sessionReattachStore)
	if !ok {
		return domain.ErrAdmissionRequired
	}
	return store.ValidateSessionReattach(ctx, permit, generation, m.clock().UTC())
}
