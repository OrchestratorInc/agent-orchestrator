package store

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// WorkflowPlanRetirement 由已确认候选与真实未释放资源派生，不存第二份完成状态。
type WorkflowPlanRetirement struct {
	AttemptID        domain.WorkflowAttemptID
	SessionID        domain.SessionID
	Generation       string
	ArtifactRevision int64
}

// WorkflowPlanRetirements 在当前 Leader 门禁内读取收尾队列，重启继续原任务，不再 Spawn。
func (s *Store) WorkflowPlanRetirements(ctx context.Context, f domain.SchedulerFence, now time.Time) ([]WorkflowPlanRetirement, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result []WorkflowPlanRetirement
	err := s.inSchedulerTx(ctx, "read plan retirements", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, f, now, false); err != nil {
			return err
		}
		rows, err := q.ListWorkflowPlanRetirements(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.SessionID == nil {
				return domain.ErrDeliveryBlocked
			}
			result = append(result, WorkflowPlanRetirement{AttemptID: row.AttemptID, SessionID: *row.SessionID, Generation: row.ControllerGeneration, ArtifactRevision: row.ArtifactRevision})
		}
		return nil
	})
	return result, err
}
