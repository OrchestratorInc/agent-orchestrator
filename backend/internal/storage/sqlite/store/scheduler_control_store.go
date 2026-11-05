package store

import (
	"context"
	"encoding/json"
	"math"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ListSchedulerOccupancy 共用准入的实际占位查询，保留旧会话和 UNKNOWN，不凭展示状态释放。
func (s *Store) ListSchedulerOccupancy(ctx context.Context) ([]domain.AgentHarness, error) {
	rows, err := s.qr.ListSchedulerOccupancy(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]domain.AgentHarness, len(rows))
	for i, row := range rows {
		out[i] = domain.AgentHarness(row)
	}
	return out, nil
}

// SetWorkflowScheduling 在同一 writer 事务内保留公平游标并修改暂停位，Operator 无需等待 Leader 恢复才能暂停。
func (s *Store) SetWorkflowScheduling(ctx context.Context, id domain.WorkflowID, paused bool, expected int64, change domain.PolicyChange) (domain.SchedulerWorkflowState, bool, error) {
	if id == "" || expected < 0 || expected == math.MaxInt64 {
		return domain.SchedulerWorkflowState{}, false, domain.ErrSchedulerPolicy
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	state := domain.SchedulerWorkflowState{WorkflowID: id, Weight: 1}
	changed := false
	err := s.inSchedulerTx(ctx, "set workflow scheduling", func(q *gen.Queries) error {
		replay, err := policyChangeReplay(ctx, q, "resource", "workflow:"+string(id), change, &state)
		if err != nil || replay {
			return err
		}
		if _, err := q.GetWorkflow(ctx, id); err != nil {
			return err
		}
		rows, err := q.ListSchedulerWorkflowStates(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.WorkflowID == string(id) {
				state = domain.SchedulerWorkflowState{WorkflowID: id, Weight: r.Weight, Deficit: r.Deficit, CandidateOffset: r.CandidateOffset, Paused: r.Paused == 1, Revision: r.Revision}
				break
			}
		}
		if state.Revision != expected {
			return domain.ErrSchedulerPolicy
		}
		state.Paused, state.Revision = paused, expected+1
		pausedValue := int64(0)
		if paused {
			pausedValue = 1
		}
		n, err := q.PutSchedulerWorkflowState(ctx, gen.PutSchedulerWorkflowStateParams{WorkflowID: string(id), Deficit: state.Deficit, CandidateOffset: state.CandidateOffset, Weight: state.Weight, Paused: pausedValue, NextRevision: state.Revision, ExpectedRevision: expected})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerPolicy
		}
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if err := insertPolicyChange(ctx, q, "resource", "workflow:"+string(id), string(data), change); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return state, changed && err == nil, err
}
