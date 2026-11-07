package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// RetireWorkflowSession 终止已结算 Attempt 的实际执行，保留原会话、工作树和工件供审计。
// 与恢复/界面切换共享 operation 锁，不能把停止失败记作终止。
func (m *Manager) RetireWorkflowSession(ctx context.Context, id domain.SessionID, generation string) error {
	if err := m.beginAgentOperation(ctx, id, agentOperationKill); err != nil {
		return err
	}
	defer m.endAgentOperation(id, agentOperationKill)
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !found || generation == "" || domain.SessionExecutionGeneration(rec) != generation {
		return ErrSwitchSourceStopUnconfirmed
	}
	if rec.IsTerminated {
		return nil
	}
	reader, ok := m.store.(interface {
		GetWorkflowAttemptBindingBySession(context.Context, domain.SessionID) (domain.WorkflowAttemptBinding, bool, error)
		GetWorkflowAttempt(context.Context, domain.WorkflowAttemptID) (domain.WorkflowAttempt, bool, error)
	})
	if !ok {
		return fmt.Errorf("workflow retirement identity unavailable")
	}
	b, found, err := reader.GetWorkflowAttemptBindingBySession(ctx, id)
	if err != nil {
		return err
	}
	if !found || b.ControllerGeneration != generation {
		return ErrSwitchSourceStopUnconfirmed
	}
	a, found, err := reader.GetWorkflowAttempt(ctx, b.AttemptID)
	if err != nil {
		return err
	}
	if !found || a.SessionID == nil || *a.SessionID != id || (a.State != domain.WorkflowAttemptStateSucceeded && a.State != domain.WorkflowAttemptStateFailed) {
		return ErrSwitchSourceStopUnconfirmed
	}
	if active, err := m.hasActiveInterfaceTransition(ctx, id); err != nil {
		return err
	} else if active {
		return ErrInterfaceTransitionInProgress
	}
	if err := m.releaseTerminalRuntime(ctx, rec); err != nil {
		return err
	}
	if err := m.stopAgentController(ctx, rec); err != nil {
		return err
	}
	if err := m.terminateReviewer(ctx, id, "workflow plan confirmed"); err != nil {
		return err
	}
	release, err := m.beginShellTerminalTeardown(ctx, id)
	if err != nil {
		return err
	}
	if release != nil {
		defer release()
	}
	m.stopPreviewBestEffort(ctx, id)
	m.destroyBrowserBestEffort(ctx, id)
	return m.recordTermination(ctx, id, false)
}
