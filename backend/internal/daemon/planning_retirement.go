package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// workflowSessionRetirer 使用实际 Manager 关停执行，禁止通过修改 Session 行伪造物理空位。
type workflowSessionRetirer interface {
	RetireWorkflowSession(context.Context, domain.SessionID, string) error
}

// retirePlanSessions 只把实际停止成功的任务交给冻结；失败任务保留持久占位并在下轮重试。
func (m *m3Services) retirePlanSessions(ctx context.Context, f domain.SchedulerFence) ([]store.WorkflowPlanRetirement, error) {
	jobs, err := m.store.WorkflowPlanRetirements(ctx, f, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	var retired []store.WorkflowPlanRetirement
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if m.planRetirer == nil {
			return nil, domain.ErrDeliveryBlocked
		}
		if err := m.planRetirer.RetireWorkflowSession(ctx, job.SessionID, job.Generation); err != nil {
			if ctx.Err() != nil || errors.Is(err, domain.ErrSchedulerFence) {
				return nil, err
			}
			slog.WarnContext(ctx, "workflow retirement deferred", "attemptId", job.AttemptID, "sessionId", job.SessionID, "err", err)
			continue
		}
		retired = append(retired, job)
	}
	return retired, nil
}

// freezePlanArtifacts 只冻结本轮确认停止的任务；单项失败保留路径占位，不阻断其他任务。
// 沿用停止前读取的任务列表，避免物理槽释放后任务从收尾查询中消失而漏掉工件。
func (m *m3Services) freezePlanArtifacts(ctx context.Context, f domain.SchedulerFence, jobs []store.WorkflowPlanRetirement) error {
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.planArtifacts == nil {
			return domain.ErrDeliveryBlocked
		}
		if _, err := m.planArtifacts.Snapshot(ctx, job.AttemptID, job.ArtifactRevision, f); err != nil {
			if ctx.Err() != nil || errors.Is(err, domain.ErrSchedulerFence) {
				return err
			}
			slog.WarnContext(ctx, "workflow artifact freeze deferred", "attemptId", job.AttemptID, "err", err)
		}
	}
	return nil
}
