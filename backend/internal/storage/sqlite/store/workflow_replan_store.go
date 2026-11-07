package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// FindWorkflowReplan 先按请求身份返回既有 Planner，使丢回执重放不再校验已经追加的任务 ID。
func (s *Store) FindWorkflowReplan(ctx context.Context, w domain.WorkflowID, key, fingerprint string) (domain.WorkflowTask, bool, error) {
	r, err := s.qr.FindWorkflowReplan(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowTask{}, false, nil
	}
	if err != nil {
		return domain.WorkflowTask{}, false, err
	}
	if r.WorkflowID != string(w) || r.RequestFingerprint != fingerprint {
		return domain.WorkflowTask{}, false, domain.ErrWorkflowIdempotencyConflict
	}
	return s.GetWorkflowTask(ctx, w, domain.WorkflowTaskID(r.PlannerTaskID))
}

// WorkflowReplanStop 从不可变 Planner 归属恢复可信停止来源。
func (s *Store) WorkflowReplanStop(ctx context.Context, id domain.WorkflowTaskID) (string, error) {
	r, err := s.qr.GetWorkflowReplan(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return r.StopInterventionID, err
}

// BeginWorkflowReplan 先持久化停止收尾意图再开放只读 Planner 准入；旧占位仍由真实收尾释放。
func (s *Store) BeginWorkflowReplan(ctx context.Context, in domain.WorkflowReplanInput, limits domain.WorkflowGraphLimits) (domain.WorkflowTask, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	task, created := in.Planner, false
	err := s.inSchedulerTx(ctx, "begin workflow replan", func(q *gen.Queries) error {
		prior, err := q.FindWorkflowReplan(ctx, in.IdempotencyKey)
		if err == nil {
			if prior.WorkflowID != string(in.WorkflowID) || prior.RequestFingerprint != in.RequestFingerprint {
				return domain.ErrWorkflowIdempotencyConflict
			}
			r, err := q.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{WorkflowID: in.WorkflowID, ID: domain.WorkflowTaskID(prior.PlannerTaskID)})
			if err != nil {
				return err
			}
			task = workflowTaskFromGen(r)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		w, err := q.GetWorkflow(ctx, in.WorkflowID)
		if err != nil {
			return err
		}
		if w.Revision != in.ExpectedRevision || w.State != domain.WorkflowStateActive {
			return domain.ErrWorkflowRevisionConflict
		}
		stop, err := q.GetWorkflowIntervention(ctx, in.StopInterventionID)
		if err != nil {
			return err
		}
		if stop.WorkflowID != in.WorkflowID || stop.State != domain.WorkflowInterventionStateStopConfirmed || stop.AttemptID == nil || stop.SessionID == nil {
			return domain.ErrWorkflowRevisionConflict
		}
		a, err := q.GetWorkflowAttempt(ctx, *stop.AttemptID)
		if err != nil {
			return err
		}
		b, err := q.GetWorkflowAttemptBinding(ctx, a.ID)
		if err != nil {
			return err
		}
		if a.WorkflowID != in.WorkflowID || b.State != domain.WorkflowBindingStateStopConfirmed || b.SessionID == nil || *b.SessionID != *stop.SessionID || b.ControllerGeneration != stop.ControllerGeneration {
			return domain.ErrWorkflowRevisionConflict
		}
		old, err := q.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{WorkflowID: in.WorkflowID, ID: a.TaskID})
		if err != nil {
			return err
		}
		if old.Planner {
			return domain.ErrWorkflowRevisionConflict
		}
		graph, err := loadMaterializationGraph(ctx, q, in.WorkflowID, limits.MaxTasks)
		if err != nil {
			return err
		}
		rows, err := q.ListWorkflowTasks(ctx, gen.ListWorkflowTasksParams{WorkflowID: in.WorkflowID, PageLimit: int64(limits.MaxTasks) + 1})
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.Planner && r.State != domain.WorkflowTaskStateSucceeded && r.State != domain.WorkflowTaskStateCancelled && r.State != domain.WorkflowTaskStateFailed {
				return domain.ErrWorkflowRevisionConflict
			}
		}
		if !task.Planner || task.WorkflowID != in.WorkflowID || task.State != domain.WorkflowTaskStatePending {
			return domain.ErrInvalidWorkflowTask
		}
		graph.TaskIDs = append(graph.TaskIDs, task.ID)
		graph.TaskRepositories = append(graph.TaskRepositories, in.Repositories...)
		if _, err := domain.ValidateWorkflowGraph(graph, limits); err != nil {
			return err
		}
		now := task.CreatedAt
		changed, err := q.CancelStoppedPlanAttempt(ctx, gen.CancelStoppedPlanAttemptParams{ID: a.ID, Now: sql.NullTime{Time: now, Valid: true}})
		if err != nil {
			return err
		}
		if changed != 1 {
			return domain.ErrWorkflowRevisionConflict
		}
		changed, err = q.UpdateWorkflowTaskState(ctx, gen.UpdateWorkflowTaskStateParams{WorkflowID: in.WorkflowID, TaskID: old.ID, ExpectedState: old.State, ExpectedRevision: old.Revision, NextState: domain.WorkflowTaskStateBlocked, UpdatedAt: now})
		if err != nil {
			return err
		}
		if changed != 1 {
			return domain.ErrWorkflowRevisionConflict
		}
		if err := q.InsertWorkflowTask(ctx, workflowTaskToInsert(task)); err != nil {
			return err
		}
		for _, repo := range in.Repositories {
			r, err := workflowTaskRepositoryToInsert(repo)
			if err != nil {
				return err
			}
			if err := q.InsertWorkflowTaskRepository(ctx, r); err != nil {
				return err
			}
		}
		if err := q.InsertWorkflowReplan(ctx, gen.InsertWorkflowReplanParams{PlannerTaskID: string(task.ID), WorkflowID: string(in.WorkflowID), StopInterventionID: stop.ID, StoppedAttemptID: string(a.ID), IdempotencyKey: in.IdempotencyKey, RequestFingerprint: in.RequestFingerprint}); err != nil {
			return err
		}
		changed, err = q.PauseWorkflowForPlan(ctx, gen.PauseWorkflowForPlanParams{WorkflowID: in.WorkflowID, ExpectedRevision: in.ExpectedRevision, Now: now})
		if err != nil {
			return err
		}
		if changed != 1 {
			return domain.ErrWorkflowRevisionConflict
		}
		if err := q.ResumeReplanScheduling(ctx, string(in.WorkflowID)); err != nil {
			return err
		}
		_, err = q.RefreshWorkflowReadyTasks(ctx, gen.RefreshWorkflowReadyTasksParams{WorkflowID: in.WorkflowID, UpdatedAt: now})
		created = err == nil
		return err
	})
	return task, created, err
}
