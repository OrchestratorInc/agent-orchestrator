package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// SaveWorkflowPlan 在同一 writer 快照保存候选及状态摘要；重放不新增候选。
func (s *Store) SaveWorkflowPlan(ctx context.Context, p domain.WorkflowPlanCandidate) (domain.WorkflowPlanCandidate, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inSchedulerTx(ctx, "save workflow plan", func(q *gen.Queries) error {
		prior, err := q.FindWorkflowPlanCandidate(ctx, p.IdempotencyKey)
		if err == nil {
			if prior.RequestFingerprint != p.RequestFingerprint || prior.WorkflowID != string(p.WorkflowID) {
				return domain.ErrWorkflowIdempotencyConflict
			}
			p, err = planFromGen(prior)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		p.StateFingerprint, _, err = validatePlanFacts(ctx, q, p, p.CreatedAt)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(p.Patch)
		if err != nil {
			return err
		}
		return q.InsertWorkflowPlanCandidate(ctx, gen.InsertWorkflowPlanCandidateParams{ID: p.ID, WorkflowID: string(p.WorkflowID), SourceAttemptID: string(p.SourceAttemptID), SourceTurnID: p.SourceTurnID, StopInterventionID: p.StopInterventionID, ExpectedRevision: p.ExpectedRevision, Digest: p.Digest, DefinitionJson: string(raw), IdempotencyKey: p.IdempotencyKey, RequestFingerprint: p.RequestFingerprint, StateFingerprint: p.StateFingerprint, CreatedAt: p.CreatedAt.Format(time.RFC3339Nano)})
	})
	return p, err
}

// GetWorkflowPlan 只读取给定 Workflow 的不可变候选。
func (s *Store) GetWorkflowPlan(ctx context.Context, w domain.WorkflowID, id string) (domain.WorkflowPlanCandidate, error) {
	r, err := s.qr.GetWorkflowPlanCandidate(ctx, gen.GetWorkflowPlanCandidateParams{WorkflowID: string(w), ID: id})
	if err != nil {
		return domain.WorkflowPlanCandidate{}, err
	}
	return planFromGen(r)
}

// FindWorkflowPlan 按全局幂等键恢复既有结果。
func (s *Store) FindWorkflowPlan(ctx context.Context, key string) (domain.WorkflowPlanCandidate, bool, error) {
	r, err := s.qr.FindWorkflowPlanCandidate(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowPlanCandidate{}, false, nil
	}
	if err != nil {
		return domain.WorkflowPlanCandidate{}, false, err
	}
	p, err := planFromGen(r)
	return p, true, err
}

// ListWorkflowPlans 重连只取快照，不再次调用模型。
func (s *Store) ListWorkflowPlans(ctx context.Context, w domain.WorkflowID) ([]domain.WorkflowPlanCandidate, error) {
	rows, err := s.qr.ListWorkflowPlanCandidates(ctx, string(w))
	if err != nil {
		return nil, err
	}
	result := make([]domain.WorkflowPlanCandidate, 0, len(rows))
	for _, r := range rows {
		p, err := planFromGen(r)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, nil
}

// ConfirmWorkflowPlan 与调度 writer 串行验证来源、状态摘要和合并 DAG；确认不隐式恢复执行。
func (s *Store) ConfirmWorkflowPlan(ctx context.Context, p domain.WorkflowPlanCandidate, limits domain.WorkflowGraphLimits, now time.Time) (domain.WorkflowPlanCandidate, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inSchedulerTx(ctx, "confirm workflow plan", func(q *gen.Queries) error {
		row, err := q.GetWorkflowPlanCandidate(ctx, gen.GetWorkflowPlanCandidateParams{WorkflowID: string(p.WorkflowID), ID: p.ID})
		if err != nil {
			return err
		}
		if row.Digest != p.Digest || row.ExpectedRevision != p.ExpectedRevision {
			return domain.ErrWorkflowRevisionConflict
		}
		p, err = planFromGen(row)
		if err != nil {
			return err
		}
		if p.ConfirmedRevision != 0 {
			return nil
		}
		fingerprint, stopped, err := validatePlanFacts(ctx, q, p, now)
		if err != nil {
			return err
		}
		if fingerprint != p.StateFingerprint {
			return domain.ErrWorkflowRevisionConflict
		}
		graph, err := loadMaterializationGraph(ctx, q, p.WorkflowID, limits.MaxTasks)
		if err != nil {
			return err
		}
		rows, err := q.ListWorkflowTasks(ctx, gen.ListWorkflowTasksParams{WorkflowID: p.WorkflowID, PageLimit: int64(limits.MaxTasks) + 1})
		if err != nil {
			return err
		}
		tasks := make([]domain.WorkflowTask, 0, len(rows))
		for _, r := range rows {
			tasks = append(tasks, workflowTaskFromGen(r))
		}
		if err := domain.ValidateWorkflowPlanPatch(graph, tasks, p.Patch, stopped, limits); err != nil {
			return err
		}
		for _, task := range p.Patch.Tasks {
			if err := q.InsertWorkflowTask(ctx, workflowTaskToInsert(task)); err != nil {
				return err
			}
		}
		for _, edge := range p.Patch.Dependencies {
			if err := q.InsertWorkflowEdge(ctx, gen.InsertWorkflowEdgeParams{WorkflowID: p.WorkflowID, FromTaskID: edge.FromTaskID, ToTaskID: edge.ToTaskID}); err != nil {
				return err
			}
		}
		for _, repo := range p.Patch.TaskRepositories {
			r, err := workflowTaskRepositoryToInsert(repo)
			if err != nil {
				return err
			}
			if err := q.InsertWorkflowTaskRepository(ctx, r); err != nil {
				return err
			}
		}
		source, err := q.GetWorkflowAttempt(ctx, p.SourceAttemptID)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			next := domain.WorkflowTaskState("")
			if task.ID == source.TaskID && task.State != domain.WorkflowTaskStateSucceeded {
				changed, err := q.FinishPlanningAttempt(ctx, gen.FinishPlanningAttemptParams{ID: source.ID, Now: sql.NullTime{Time: now, Valid: true}})
				if err != nil {
					return err
				}
				if changed != 1 {
					return domain.ErrWorkflowRevisionConflict
				}
				next = domain.WorkflowTaskStateSucceeded
			}
			for _, id := range p.Patch.RetireTaskIDs {
				if id == task.ID {
					next = domain.WorkflowTaskStateCancelled
				}
			}
			if next != "" {
				changed, err := q.UpdateWorkflowTaskState(ctx, gen.UpdateWorkflowTaskStateParams{WorkflowID: p.WorkflowID, TaskID: task.ID, ExpectedState: task.State, ExpectedRevision: task.Revision, NextState: next, UpdatedAt: now})
				if err != nil {
					return err
				}
				if changed != 1 {
					return domain.ErrWorkflowRevisionConflict
				}
			}
		}
		changed, err := q.PauseWorkflowForPlan(ctx, gen.PauseWorkflowForPlanParams{WorkflowID: p.WorkflowID, ExpectedRevision: p.ExpectedRevision, Now: now})
		if err != nil {
			return err
		}
		if changed != 1 {
			return domain.ErrWorkflowRevisionConflict
		}
		p.ConfirmedRevision = p.ExpectedRevision + 1
		if err := q.PausePlanScheduling(ctx, string(p.WorkflowID)); err != nil {
			return err
		}
		if _, err := q.RefreshWorkflowReadyTasks(ctx, gen.RefreshWorkflowReadyTasksParams{WorkflowID: p.WorkflowID, UpdatedAt: now}); err != nil {
			return err
		}
		_, err = q.ConfirmWorkflowPlanCandidate(ctx, gen.ConfirmWorkflowPlanCandidateParams{ID: p.ID, ConfirmedRevision: p.ConfirmedRevision})
		return err
	})
	return p, err
}

// planFromGen 解码服务端存储，不把数据库字段解释为可执行指令。
func planFromGen(r gen.WorkflowPlanCandidate) (domain.WorkflowPlanCandidate, error) {
	p := domain.WorkflowPlanCandidate{ID: r.ID, WorkflowID: domain.WorkflowID(r.WorkflowID), SourceAttemptID: domain.WorkflowAttemptID(r.SourceAttemptID), SourceTurnID: r.SourceTurnID, StopInterventionID: r.StopInterventionID, ExpectedRevision: r.ExpectedRevision, Digest: r.Digest, IdempotencyKey: r.IdempotencyKey, RequestFingerprint: r.RequestFingerprint, StateFingerprint: r.StateFingerprint, ConfirmedRevision: r.ConfirmedRevision}
	var err error
	p.CreatedAt, err = time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(r.DefinitionJson), &p.Patch)
	return p, err
}

// validatePlanFacts 只使用事务内的持久 Chat/Attempt/停止证明；心跳续期不改变候选，执行状态变化会失效。
func validatePlanFacts(ctx context.Context, q *gen.Queries, p domain.WorkflowPlanCandidate, now time.Time) (string, domain.WorkflowTaskID, error) {
	refuse := domain.ErrWorkflowRevisionConflict
	w, err := q.GetWorkflow(ctx, p.WorkflowID)
	if err != nil {
		return "", "", err
	}
	if w.Revision != p.ExpectedRevision || (w.State != domain.WorkflowStateActive && w.State != domain.WorkflowStatePaused) {
		return "", "", refuse
	}
	a, err := q.GetWorkflowAttempt(ctx, p.SourceAttemptID)
	if err != nil {
		return "", "", err
	}
	t, err := q.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{WorkflowID: p.WorkflowID, ID: a.TaskID})
	if err != nil {
		return "", "", err
	}
	b, err := q.GetWorkflowAttemptBinding(ctx, a.ID)
	if err != nil {
		return "", "", err
	}
	turn, err := q.SelectConversationTurnByID(ctx, p.SourceTurnID)
	if err != nil {
		return "", "", err
	}
	if !t.Planner || a.WorkflowID != p.WorkflowID || a.SessionID == nil || b.SessionID == nil || *a.SessionID != *b.SessionID || turn.HandledBySessionID != *a.SessionID || turn.State != domain.TurnStateCompleted || turn.RolledBackAt.Valid || b.ControllerGeneration != turn.ControllerGeneration {
		return "", "", refuse
	}
	if a.State != domain.WorkflowAttemptStateSucceeded && (a.State != domain.WorkflowAttemptStateRunning && a.State != domain.WorkflowAttemptStateClaimed || !a.LeaseUntil.Valid || !a.LeaseUntil.Time.After(now)) {
		return "", "", refuse
	}
	if b.State != domain.WorkflowBindingStateBoundReady && b.State != domain.WorkflowBindingStateRunning {
		return "", "", refuse
	}
	newer, err := q.HasNewerPlannerTurn(ctx, gen.HasNewerPlannerTurnParams{SessionID: *a.SessionID, RequestedAt: turn.RequestedAt, TurnID: turn.ID})
	if err != nil {
		return "", "", err
	}
	if newer {
		return "", "", refuse
	}
	var stopped domain.WorkflowTaskID
	replan, replanErr := q.GetWorkflowReplan(ctx, string(t.ID))
	if replanErr != nil && !errors.Is(replanErr, sql.ErrNoRows) {
		return "", "", replanErr
	}
	if (replanErr == nil && replan.StopInterventionID != p.StopInterventionID) || (errors.Is(replanErr, sql.ErrNoRows) && p.StopInterventionID != "") {
		return "", "", refuse
	}
	if p.StopInterventionID != "" {
		stop, err := q.GetWorkflowIntervention(ctx, p.StopInterventionID)
		if err != nil {
			return "", "", err
		}
		if stop.WorkflowID != p.WorkflowID || stop.State != domain.WorkflowInterventionStateStopConfirmed || stop.AttemptID == nil || stop.SessionID == nil || turn.RequestedAt.Before(stop.UpdatedAt) {
			return "", "", refuse
		}
		sa, err := q.GetWorkflowAttempt(ctx, *stop.AttemptID)
		if err != nil {
			return "", "", err
		}
		sb, err := q.GetWorkflowAttemptBinding(ctx, *stop.AttemptID)
		if err != nil {
			return "", "", err
		}
		if sa.State != domain.WorkflowAttemptStateFailed || sa.ErrorCode != "operator_replanned" || sb.State != domain.WorkflowBindingStateStopConfirmed || sb.SessionID == nil || *sb.SessionID != *stop.SessionID || sb.ControllerGeneration != stop.ControllerGeneration {
			return "", "", refuse
		}
		session, err := q.GetSession(ctx, *stop.SessionID)
		if err != nil {
			return "", "", err
		}
		if !session.IsTerminated {
			return "", "", refuse
		}
		stopped = stop.TaskID
	}
	tasks, err := q.ListWorkflowTasks(ctx, gen.ListWorkflowTasksParams{WorkflowID: p.WorkflowID, PageLimit: 10001})
	if err != nil {
		return "", "", err
	}
	attempts, err := q.WorkflowPlanAttemptFacts(ctx, p.WorkflowID)
	if err != nil {
		return "", "", err
	}
	for _, a := range attempts {
		if a.State == domain.WorkflowAttemptStateUnknown {
			return "", "", refuse
		}
		for _, id := range p.Patch.RetireTaskIDs {
			if a.TaskID == id && id != stopped && (a.State == domain.WorkflowAttemptStateRunning || a.State == domain.WorkflowAttemptStateClaimed || a.State == domain.WorkflowAttemptStateSettling) {
				return "", "", refuse
			}
		}
	}
	raw, err := json.Marshal([]any{tasks, attempts})
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), stopped, nil
}
