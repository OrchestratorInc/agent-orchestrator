package store

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var _ ports.SchedulerStore = (*Store)(nil)

// RecordSchedulerDecision 独立提交拒绝审计；失败返回错误，不冒称回滚事务留下了记录。
func (s *Store) RecordSchedulerDecision(ctx context.Context, d domain.SchedulerDecision) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return insertSchedulerDecision(ctx, s.qw, d)
}

// insertSchedulerDecision 仅接受机器原因码，以免自由文本泄漏到决策记录。
func insertSchedulerDecision(ctx context.Context, q *gen.Queries, d domain.SchedulerDecision) error {
	if !domain.ValidSchedulerReason(d.ReasonCode) || d.CreatedAt.IsZero() {
		return domain.ErrSchedulerPolicy
	}
	data, err := json.Marshal(d.PolicyRevisions)
	if err != nil {
		return err
	}
	return q.InsertSchedulerDecision(ctx, gen.InsertSchedulerDecisionParams{AdmissionID: d.AdmissionID, WorkflowID: string(d.WorkflowID), TaskID: string(d.TaskID), OwnerID: d.Fence.OwnerID, Epoch: d.Fence.Epoch, ReasonCode: d.ReasonCode, PolicyRevisionsJson: string(data), CreatedAt: d.CreatedAt.UTC()})
}

// ListSchedulerDecisions 以有界增量分页返回不含凭据的决策事实。
func (s *Store) ListSchedulerDecisions(ctx context.Context, after, limit int64) ([]domain.SchedulerDecision, error) {
	if after < 0 || limit < 1 || limit > 1000 {
		return nil, domain.ErrSchedulerPolicy
	}
	rows, err := s.qr.ListSchedulerDecisions(ctx, gen.ListSchedulerDecisionsParams{ID: after, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]domain.SchedulerDecision, 0, len(rows))
	for _, r := range rows {
		d := domain.SchedulerDecision{ID: r.ID, AdmissionID: r.AdmissionID, WorkflowID: domain.WorkflowID(r.WorkflowID), TaskID: domain.WorkflowTaskID(r.TaskID), Fence: domain.SchedulerFence{OwnerID: r.OwnerID, Epoch: r.Epoch}, ReasonCode: r.ReasonCode, CreatedAt: r.CreatedAt}
		if err := json.Unmarshal([]byte(r.PolicyRevisionsJson), &d.PolicyRevisions); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// PutSchedulerWorkflowState 以当前任期和 revision CAS 保留公平队列的状态。
func (s *Store) PutSchedulerWorkflowState(ctx context.Context, v domain.SchedulerWorkflowState, expected int64, f domain.SchedulerFence, now time.Time) (bool, error) {
	if v.WorkflowID == "" || v.Deficit < 0 || v.CandidateOffset < 0 || v.Weight < 1 || v.Weight > 5 || expected < 0 || expected == math.MaxInt64 {
		return false, domain.ErrSchedulerPolicy
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inSchedulerTx(ctx, "update scheduler workflow", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, f, now, true); err != nil {
			return err
		}
		var paused int64
		if v.Paused {
			paused = 1
		}
		n, err := q.PutSchedulerWorkflowState(ctx, gen.PutSchedulerWorkflowStateParams{WorkflowID: string(v.WorkflowID), Deficit: v.Deficit, CandidateOffset: v.CandidateOffset, Weight: v.Weight, Paused: paused, NextRevision: expected + 1, ExpectedRevision: expected})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerPolicy
		}
		return nil
	})
	return err == nil, err
}

// ListSchedulerWorkflowStates 返回可跨重启恢复的公平状态。
func (s *Store) ListSchedulerWorkflowStates(ctx context.Context) ([]domain.SchedulerWorkflowState, error) {
	rows, err := s.qr.ListSchedulerWorkflowStates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SchedulerWorkflowState, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.SchedulerWorkflowState{WorkflowID: domain.WorkflowID(r.WorkflowID), Deficit: r.Deficit, CandidateOffset: r.CandidateOffset, Weight: r.Weight, Paused: r.Paused == 1, Revision: r.Revision})
	}
	return out, nil
}

// SetSchedulerCursor 只允许当前 Leader 推进已恢复队列的轮转游标。
func (s *Store) SetSchedulerCursor(ctx context.Context, f domain.SchedulerFence, revision int64, cursor string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.SetSchedulerCursor(ctx, gen.SetSchedulerCursorParams{OwnerID: f.OwnerID, Epoch: f.Epoch, Revision: revision, Cursor: cursor, Now: now.UTC()})
	if err == nil && n != 1 {
		err = domain.ErrSchedulerFence
	}
	return n == 1 && err == nil, err
}

// PutSchedulerRetryDue 持久化受任期保护的冷却时间，不改变 Task 或 Attempt 状态。
func (s *Store) PutSchedulerRetryDue(ctx context.Context, v domain.SchedulerRetryDue, f domain.SchedulerFence, now time.Time) error {
	if !domain.ValidSchedulerReason(v.ReasonCode) || v.PolicyRevision < 1 || v.NextEligibleAt.IsZero() {
		return domain.ErrSchedulerPolicy
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inSchedulerTx(ctx, "update scheduler retry", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, f, now, true); err != nil {
			return err
		}
		a, err := q.GetWorkflowAttempt(ctx, v.AttemptID)
		if err != nil {
			return err
		}
		latest, err := q.GetNextWorkflowAttemptNo(ctx, gen.GetNextWorkflowAttemptNoParams{WorkflowID: v.WorkflowID, TaskID: v.TaskID})
		if err != nil {
			return err
		}
		if a.WorkflowID != v.WorkflowID || a.TaskID != v.TaskID || a.AttemptNo != latest-1 || a.State != domain.WorkflowAttemptStateFailed {
			return domain.ErrSchedulerAdmissionConflict
		}
		return q.PutSchedulerRetryDue(ctx, gen.PutSchedulerRetryDueParams{WorkflowID: string(v.WorkflowID), TaskID: string(v.TaskID), AttemptID: string(v.AttemptID), Harness: string(v.Harness), NextEligibleAt: v.NextEligibleAt.UTC(), ReasonCode: v.ReasonCode, PolicyRevision: v.PolicyRevision})
	})
}

// ListSchedulerRetryDue 为 Scheduler 重启恢复提供已持久化的最早重试时间。
func (s *Store) ListSchedulerRetryDue(ctx context.Context) ([]domain.SchedulerRetryDue, error) {
	rows, err := s.qr.ListSchedulerRetryDue(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SchedulerRetryDue, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.SchedulerRetryDue{WorkflowID: domain.WorkflowID(r.WorkflowID), TaskID: domain.WorkflowTaskID(r.TaskID), AttemptID: domain.WorkflowAttemptID(r.AttemptID), Harness: domain.AgentHarness(r.Harness), NextEligibleAt: r.NextEligibleAt, ReasonCode: r.ReasonCode, PolicyRevision: r.PolicyRevision})
	}
	return out, nil
}
