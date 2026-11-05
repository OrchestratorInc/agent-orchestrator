package execution

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// permitStore 是真实 SQLite 执行许可边界；纯 Gateway 单元替身不伪造持久许可。
type permitStore interface {
	GetSchedulerLeader(context.Context) (domain.SchedulerLeader, error)
	LoadExecutionPermit(context.Context, domain.WorkflowAttemptID, domain.SchedulerFence, time.Time) (domain.ExecutionPermit, error)
	ValidateExecutionPermit(context.Context, domain.ExecutionPermit, time.Time) error
}

// actionContext 校验调用者原许可，再为目标执行取同任期快照；跨任务消息不能借此跨 Leader。
func (c *Coordinator) actionContext(ctx context.Context, id domain.WorkflowAttemptID) (context.Context, error) {
	store, ok := c.store.(permitStore)
	if !ok {
		return ctx, nil
	}
	if source, ok := ports.ExecutionPermitFromContext(ctx); ok && source.AttemptID != id {
		if err := store.ValidateExecutionPermit(ctx, source, c.now().UTC()); err != nil {
			return ctx, err
		}
		target, err := store.LoadExecutionPermit(ctx, id, source.Fence, c.now().UTC())
		if err != nil {
			return ctx, err
		}
		ctx = ports.WithExecutionPermit(ctx, target)
	}
	ctx, err := c.executionContext(ctx, id)
	if err != nil {
		return ctx, err
	}
	p, _ := ports.ExecutionPermitFromContext(ctx)
	return ctx, store.ValidateExecutionPermit(ctx, p, c.now().UTC())
}

// executionContext 捕获一次执行的任期；已有调用者快照不得刷新成新 Leader 的身份。
func (c *Coordinator) executionContext(ctx context.Context, id domain.WorkflowAttemptID) (context.Context, error) {
	store, ok := c.store.(permitStore)
	if !ok {
		return ctx, nil
	}
	if p, ok := ports.ExecutionPermitFromContext(ctx); ok {
		if p.AttemptID != id {
			return ctx, domain.ErrAdmissionRequired
		}
		return ctx, nil
	}
	if c.executionFence == nil {
		return ctx, domain.ErrSchedulerFence
	}
	fence, err := c.executionFence(ctx)
	if err != nil {
		return ctx, err
	}
	p, err := store.LoadExecutionPermit(ctx, id, fence, c.now().UTC())
	if err != nil {
		return ctx, err
	}
	return ports.WithExecutionPermit(ctx, p), nil
}

// bindingPermitContext 跟随本次调用已取得的 Binding CAS 结果，保留原始任期及 Attempt 快照。
func bindingPermitContext(ctx context.Context, b domain.WorkflowAttemptBinding) context.Context {
	p, ok := ports.ExecutionPermitFromContext(ctx)
	if !ok {
		return ctx
	}
	p.BindingRevision = b.Revision
	p.Generation = b.ControllerGeneration
	return ports.WithExecutionPermit(ctx, p)
}

// refreshExecutionContext 在已接受的 Session 或绑定落库后刷新快照，但绝不跨任期认领迟到回执。
func (c *Coordinator) refreshExecutionContext(ctx context.Context, id domain.WorkflowAttemptID) (context.Context, error) {
	store, ok := c.store.(permitStore)
	if !ok {
		return ctx, nil
	}
	old, ok := ports.ExecutionPermitFromContext(ctx)
	if !ok {
		return c.executionContext(ctx, id)
	}
	p, err := store.LoadExecutionPermit(ctx, id, old.Fence, c.now().UTC())
	if err != nil {
		return ctx, err
	}
	if p.WorkerID != old.WorkerID || p.LeaseToken != old.LeaseToken || !p.LeaseUntil.Equal(old.LeaseUntil) || p.AttemptState != old.AttemptState {
		return ctx, domain.ErrSchedulerFence
	}
	return ports.WithExecutionPermit(ctx, p), nil
}
