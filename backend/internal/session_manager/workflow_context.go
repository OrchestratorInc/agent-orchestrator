package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// workflowAttemptBindingStore is the narrow durable surface a RESUMED session needs
// to recover which workflow attempt it belongs to.
//
// It is a local optional interface rather than a widening of Store: only a store that
// actually owns workflow bindings can answer it, and a deployment without M2 wiring
// keeps working with no binding at all.
type workflowAttemptBindingStore interface {
	GetWorkflowAttemptBindingBySession(
		ctx context.Context,
		sessionID domain.SessionID,
	) (domain.WorkflowAttemptBinding, bool, error)
}

// workflowAttemptLeaseStore 只读取内部 Attempt，以验证恢复身份的状态与期限。
// 读取租约不会授权把租约放入恢复上下文或交给进程。
type workflowAttemptLeaseStore interface {
	GetWorkflowAttempt(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) (domain.WorkflowAttempt, bool, error)
}

// workflowSpawnContext 从持久事实恢复 Session 的非秘密 Workflow 身份。
// 普通 Session 没有绑定时返回 nil；已存在的绑定必须通过归属、generation、
// 状态和租约期限校验，否则拒绝恢复，不能降级成普通 Session 继续启动。
func (m *Manager) workflowSpawnContext(
	ctx context.Context,
	rec domain.SessionRecord,
) (*ports.WorkflowSpawnContext, error) {
	bindings, ok := m.store.(workflowAttemptBindingStore)
	if !ok {
		return nil, nil
	}
	binding, found, err := bindings.GetWorkflowAttemptBindingBySession(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	if rec.IsTerminated || strings.TrimSpace(string(rec.ID)) == "" ||
		binding.SessionID == nil || *binding.SessionID != rec.ID ||
		(binding.State != domain.WorkflowBindingStateBoundReady && binding.State != domain.WorkflowBindingStateRunning && binding.State != domain.WorkflowBindingStateReconcileRequired && binding.State != domain.WorkflowBindingStateRecoveryUncertain) ||
		strings.TrimSpace(binding.ControllerGeneration) == "" ||
		strings.TrimSpace(rec.Metadata.ControllerGeneration) == "" ||
		binding.ControllerGeneration != rec.Metadata.ControllerGeneration ||
		strings.TrimSpace(string(binding.WorkflowID)) == "" ||
		strings.TrimSpace(string(binding.TaskID)) == "" ||
		strings.TrimSpace(string(binding.AttemptID)) == "" {
		return nil, errors.New("workflow session binding is not valid for resume")
	}
	// 工作区曾缺失会留下恢复不确定状态；显式恢复必须重新拿到真实工作区证据，
	// 而不是要求先有一个已启动的 Controller 才能把状态恢复为 ready。
	if binding.State == domain.WorkflowBindingStateReconcileRequired || binding.State == domain.WorkflowBindingStateRecoveryUncertain {
		if _, ok := ports.ExecutionPermitFromContext(ctx); !ok {
			return nil, domain.ErrAdmissionRequired
		}
		if _, err := m.observeSessionWorktrees(ctx, rec); err != nil {
			return nil, err
		}
	}
	leases, ok := m.store.(workflowAttemptLeaseStore)
	if !ok {
		return nil, errors.New("workflow attempt validation is unavailable")
	}
	attempt, found, err := leases.GetWorkflowAttempt(ctx, binding.AttemptID)
	if err != nil {
		return nil, err
	}
	if !found || attempt.ID != binding.AttemptID ||
		attempt.WorkflowID != binding.WorkflowID || attempt.TaskID != binding.TaskID ||
		(attempt.State != domain.WorkflowAttemptStateClaimed && attempt.State != domain.WorkflowAttemptStateRunning) ||
		!workflowLeaseValid(attempt, m.clock()) {
		return nil, errors.New("workflow attempt is not valid for resume")
	}
	if permits, ok := m.store.(interface {
		ValidateExecutionPermit(context.Context, domain.ExecutionPermit, time.Time) error
	}); ok {
		permit, ok := ports.ExecutionPermitFromContext(ctx)
		if !ok || permit.AttemptID != binding.AttemptID || permit.SessionID != rec.ID {
			return nil, domain.ErrAdmissionRequired
		}
		if err := permits.ValidateExecutionPermit(ctx, permit, m.clock().UTC()); err != nil {
			return nil, err
		}
	}
	return &ports.WorkflowSpawnContext{
		WorkflowID: binding.WorkflowID,
		TaskID:     binding.TaskID,
		AttemptID:  binding.AttemptID,
	}, nil
}

// workflowLeaseValid 只验证内部租约尚未失效；结果不包含可外发凭据。
func workflowLeaseValid(attempt domain.WorkflowAttempt, now time.Time) bool {
	if strings.TrimSpace(attempt.LeaseToken) == "" || attempt.LeaseUntil == nil {
		return false
	}
	return attempt.LeaseUntil.After(now.UTC())
}
