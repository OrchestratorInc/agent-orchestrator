package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ListIntegrationPlans 读取冻结计划历史，状态不代表此刻目标 SHA 仍相同。
func (s *Store) ListIntegrationPlans(ctx context.Context, w domain.WorkflowID) ([]domain.IntegrationPlan, error) {
	rows, err := s.qr.ListIntegrationPlans(ctx, string(w))
	if err != nil {
		return nil, err
	}
	out := make([]domain.IntegrationPlan, 0, len(rows))
	for _, r := range rows {
		out = append(out, integrationPlanFromRow(r))
	}
	return out, nil
}

// RecentCoordinationMessages 在查询层只选消息元数据，原始正文从不进入协调摘要。
func (s *Store) RecentCoordinationMessages(ctx context.Context, w domain.WorkflowID) ([]domain.CoordinationMessageView, error) {
	rows, err := s.qr.RecentCoordinationMessages(ctx, w)
	if err != nil {
		return nil, err
	}
	out := make([]domain.CoordinationMessageView, 0, len(rows))
	for _, r := range rows {
		v := domain.CoordinationMessageView{ID: r.ID, Kind: r.Kind, DeliveryState: r.DeliveryState, Revision: r.Revision, CreatedAt: r.CreatedAt}
		if r.FromTask != "" {
			id := r.FromTask
			v.TaskID = &id
		}
		if r.ToTask != nil && *r.ToTask != "" {
			v.ToTaskID = r.ToTask
		}
		out = append(out, v)
	}
	return out, nil
}

// LatestTaskSchedulingReason 复用原调度决策，不产生新的阻塞状态。
func (s *Store) LatestTaskSchedulingReason(ctx context.Context, w domain.WorkflowID, task domain.WorkflowTaskID) (string, error) {
	r, err := s.qr.LatestTaskSchedulingReason(ctx, gen.LatestTaskSchedulingReasonParams{WorkflowID: string(w), TaskID: string(task)})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return r, err
}

// ListIntegrationOperations 返回持久汇总供既有 CDC 触发的只读刷新使用。
func (s *Store) ListIntegrationOperations(ctx context.Context, w domain.WorkflowID) ([]domain.IntegrationOperation, error) {
	rows, err := s.qr.ListIntegrationOperations(ctx, string(w))
	if err != nil {
		return nil, err
	}
	out := make([]domain.IntegrationOperation, 0, len(rows))
	for _, r := range rows {
		out = append(out, integrationOperationFromRow(r))
	}
	return out, nil
}

// ListWorkflowPathReservations 白名单映射避免物理路径、任期和 generation 进入普通 HTTP 投影。
func (s *Store) ListWorkflowPathReservations(ctx context.Context, w domain.WorkflowID) ([]domain.PathReservationView, error) {
	rows, err := s.qr.ListWorkflowPathReservations(ctx, string(w))
	if err != nil {
		return nil, err
	}
	out := make([]domain.PathReservationView, 0, len(rows))
	for _, r := range rows {
		v := domain.PathReservationView{ID: r.ID, AttemptID: domain.WorkflowAttemptID(r.AttemptID), TaskID: domain.WorkflowTaskID(r.TaskID), RepoKey: domain.WorkflowRepoKey(r.RepoKey), ScopePath: r.ScopePath, SessionID: domain.SessionID(r.SessionID), LeaseUntil: r.LeaseUntil, Revision: r.Revision}
		if r.ReleasedAt.Valid {
			at := r.ReleasedAt.Time
			v.ReleasedAt = &at
		}
		out = append(out, v)
	}
	return out, nil
}
