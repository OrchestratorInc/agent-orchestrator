package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CreateWorkflow persists a complete, prevalidated graph in one transaction.
// Its deferred workflow foreign key lets the idempotency key reserve the write
// before child rows are inserted, so concurrent replays cannot duplicate a graph.
func (s *Store) CreateWorkflow(
	ctx context.Context,
	rec domain.Workflow,
	repositories []domain.WorkflowRepository,
	tasks []domain.WorkflowTask,
	edges []domain.WorkflowEdge,
	taskRepositories []domain.WorkflowTaskRepository,
	idempotencyKey string,
	requestFingerprint string,
) (domain.Workflow, bool, error) {
	if strings.TrimSpace(idempotencyKey) == "" || len(requestFingerprint) != 64 {
		return domain.Workflow{}, false, domain.ErrInvalidWorkflow
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	created := false
	result := rec
	err := s.inTx(ctx, "create workflow", func(q *gen.Queries) error {
		reserved, err := insertWorkflowIdempotencyRows(ctx, q, workflowIdempotencyInsert{
			Key: idempotencyKey, WorkflowID: rec.ID, Operation: domain.WorkflowOperationCreate,
			Fingerprint: requestFingerprint, ResultType: "workflow", ResultID: string(rec.ID),
			CreatedAt: rec.CreatedAt,
		})
		if err != nil {
			return err
		}
		if reserved == 0 {
			row, found, err := lookupWorkflowIdempotency(
				ctx, q, idempotencyKey, "", domain.WorkflowOperationCreate, requestFingerprint, "workflow",
			)
			if err != nil {
				return err
			}
			if !found {
				return domain.ErrWorkflowIdempotencyConflict
			}
			existing, err := q.GetWorkflow(ctx, row.WorkflowID)
			if err != nil {
				return err
			}
			result = workflowFromGen(existing)
			return nil
		}
		if err := rec.Validate(); err != nil {
			return err
		}
		if rec.State != domain.WorkflowStateDraft {
			return domain.ErrInvalidWorkflowTransition
		}
		if len(tasks) == 0 {
			return domain.ErrInvalidWorkflowTask
		}
		for _, repository := range repositories {
			if err := repository.Validate(); err != nil || repository.WorkflowID != rec.ID {
				return domain.ErrInvalidWorkflowRepository
			}
			project, err := q.GetProject(ctx, repository.ProjectID)
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrWorkflowProjectNotFound
			}
			if err != nil {
				return err
			}
			if project.ArchivedAt.Valid {
				return domain.ErrWorkflowProjectArchived
			}
		}
		for _, task := range tasks {
			if err := task.Validate(); err != nil || task.WorkflowID != rec.ID || task.State != domain.WorkflowTaskStatePending {
				return domain.ErrInvalidWorkflowTask
			}
		}
		for _, edge := range edges {
			if err := edge.Validate(); err != nil || edge.WorkflowID != rec.ID {
				return domain.ErrInvalidWorkflowEdge
			}
		}
		for _, binding := range taskRepositories {
			if err := binding.Validate(); err != nil || binding.WorkflowID != rec.ID {
				return domain.ErrInvalidWorkflowTaskRepository
			}
		}
		if err := q.InsertWorkflow(ctx, workflowToInsert(rec)); err != nil {
			return err
		}
		for _, repository := range repositories {
			insert, err := workflowRepositoryToInsert(repository)
			if err != nil {
				return err
			}
			if err := q.InsertWorkflowRepository(ctx, insert); err != nil {
				return err
			}
		}
		for _, task := range tasks {
			if err := q.InsertWorkflowTask(ctx, workflowTaskToInsert(task)); err != nil {
				return err
			}
		}
		for _, edge := range edges {
			if err := q.InsertWorkflowEdge(ctx, gen.InsertWorkflowEdgeParams{
				WorkflowID: edge.WorkflowID, FromTaskID: edge.FromTaskID, ToTaskID: edge.ToTaskID,
			}); err != nil {
				return err
			}
		}
		for _, binding := range taskRepositories {
			insert, err := workflowTaskRepositoryToInsert(binding)
			if err != nil {
				return err
			}
			if err := q.InsertWorkflowTaskRepository(ctx, insert); err != nil {
				return err
			}
		}
		created = true
		return nil
	})
	if errors.Is(err, domain.ErrWorkflowIdempotencyConflict) {
		row, found, lookupErr := s.lookupWorkflowIdempotency(
			ctx, idempotencyKey, "", domain.WorkflowOperationCreate, requestFingerprint, "workflow",
		)
		if lookupErr != nil {
			return domain.Workflow{}, false, lookupErr
		}
		if !found {
			return domain.Workflow{}, false, domain.ErrWorkflowIdempotencyConflict
		}
		existing, exists, readErr := s.GetWorkflow(ctx, row.WorkflowID)
		if readErr != nil {
			return domain.Workflow{}, false, readErr
		}
		if !exists {
			return domain.Workflow{}, false, domain.ErrWorkflowIdempotencyConflict
		}
		return existing, false, nil
	}
	if err != nil {
		return domain.Workflow{}, false, fmt.Errorf("create workflow %s: %w", rec.ID, err)
	}
	return result, created, nil
}

// GetWorkflowByIdempotencyKey resolves a previously committed create request.
func (s *Store) GetWorkflowByIdempotencyKey(
	ctx context.Context,
	key string,
	requestFingerprint string,
) (domain.Workflow, bool, error) {
	row, found, err := s.lookupWorkflowIdempotency(
		ctx, key, "", domain.WorkflowOperationCreate, requestFingerprint, "workflow",
	)
	if err != nil {
		return domain.Workflow{}, false, err
	}
	if !found {
		return domain.Workflow{}, false, nil
	}
	rec, exists, err := s.GetWorkflow(ctx, domain.WorkflowID(row.ResultID))
	if err != nil {
		return domain.Workflow{}, false, err
	}
	if !exists {
		return domain.Workflow{}, false, domain.ErrWorkflowIdempotencyConflict
	}
	return rec, true, nil
}

// GetWorkflow reads one workflow by its durable ID.
func (s *Store) GetWorkflow(ctx context.Context, id domain.WorkflowID) (domain.Workflow, bool, error) {
	row, err := s.qr.GetWorkflow(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Workflow{}, false, nil
	}
	if err != nil {
		return domain.Workflow{}, false, fmt.Errorf("get workflow %s: %w", id, err)
	}
	return workflowFromGen(row), true, nil
}

// ListWorkflows returns a stable page of workflows, optionally filtered by state.
func (s *Store) ListWorkflows(
	ctx context.Context,
	stateFilter domain.WorkflowState,
	limit int64,
	offset int64,
) ([]domain.Workflow, error) {
	rows, err := s.qr.ListWorkflows(ctx, gen.ListWorkflowsParams{
		StateFilter: string(stateFilter), PageOffset: offset, PageLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	items := make([]domain.Workflow, 0, len(rows))
	for _, row := range rows {
		items = append(items, workflowFromGen(row))
	}
	return items, nil
}

// GetWorkflowTask reads one task within its owning workflow.
func (s *Store) GetWorkflowTask(
	ctx context.Context,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
) (domain.WorkflowTask, bool, error) {
	row, err := s.qr.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{WorkflowID: workflowID, ID: taskID})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowTask{}, false, nil
	}
	if err != nil {
		return domain.WorkflowTask{}, false, fmt.Errorf("get workflow task %s/%s: %w", workflowID, taskID, err)
	}
	return workflowTaskFromGen(row), true, nil
}

// ListWorkflowTasks returns a stable priority-ordered page for one workflow.
func (s *Store) ListWorkflowTasks(
	ctx context.Context,
	workflowID domain.WorkflowID,
	stateFilter domain.WorkflowTaskState,
	limit int64,
	offset int64,
) ([]domain.WorkflowTask, error) {
	rows, err := s.qr.ListWorkflowTasks(ctx, gen.ListWorkflowTasksParams{
		WorkflowID: workflowID, StateFilter: string(stateFilter), PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list workflow tasks %s: %w", workflowID, err)
	}
	items := make([]domain.WorkflowTask, 0, len(rows))
	for _, row := range rows {
		items = append(items, workflowTaskFromGen(row))
	}
	return items, nil
}

// ListWorkflowRepositories returns all registered repository bindings for a workflow.
func (s *Store) ListWorkflowRepositories(ctx context.Context, workflowID domain.WorkflowID) ([]domain.WorkflowRepository, error) {
	rows, err := s.qr.ListWorkflowRepositories(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("list workflow repositories %s: %w", workflowID, err)
	}
	items := make([]domain.WorkflowRepository, 0, len(rows))
	for _, row := range rows {
		items = append(items, workflowRepositoryFromGen(row))
	}
	return items, nil
}

// ListWorkflowEdges returns dependency edges in stable source/target order.
func (s *Store) ListWorkflowEdges(ctx context.Context, workflowID domain.WorkflowID) ([]domain.WorkflowEdge, error) {
	rows, err := s.qr.ListWorkflowEdges(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("list workflow edges %s: %w", workflowID, err)
	}
	items := make([]domain.WorkflowEdge, 0, len(rows))
	for _, row := range rows {
		items = append(items, domain.WorkflowEdge{
			WorkflowID: row.WorkflowID, FromTaskID: row.FromTaskID, ToTaskID: row.ToTaskID,
		})
	}
	return items, nil
}

// ListWorkflowTaskRepositories returns repository scopes assigned to one task.
func (s *Store) ListWorkflowTaskRepositories(
	ctx context.Context,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
) ([]domain.WorkflowTaskRepository, error) {
	rows, err := s.qr.ListWorkflowTaskRepositories(ctx, gen.ListWorkflowTaskRepositoriesParams{
		WorkflowID: workflowID, TaskID: taskID,
	})
	if err != nil {
		return nil, fmt.Errorf("list workflow task repositories %s/%s: %w", workflowID, taskID, err)
	}
	items := make([]domain.WorkflowTaskRepository, 0, len(rows))
	for _, row := range rows {
		var allowedPaths []string
		if err := json.Unmarshal([]byte(row.AllowedPathsJson), &allowedPaths); err != nil {
			return nil, fmt.Errorf("decode allowed paths for workflow task %s/%s: %w", workflowID, taskID, err)
		}
		items = append(items, domain.WorkflowTaskRepository{
			WorkflowID: row.WorkflowID, TaskID: row.TaskID, RepoKey: row.RepoKey,
			AllowedPaths: allowedPaths, BaseSHA: row.BaseSha,
		})
	}
	return items, nil
}

// ListWorkflowAttempts returns attempts newest-first for one task.
func (s *Store) ListWorkflowAttempts(
	ctx context.Context,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
) ([]domain.WorkflowAttempt, error) {
	rows, err := s.qr.ListWorkflowAttempts(ctx, gen.ListWorkflowAttemptsParams{
		WorkflowID: workflowID, TaskID: taskID,
	})
	if err != nil {
		return nil, fmt.Errorf("list workflow attempts %s/%s: %w", workflowID, taskID, err)
	}
	items := make([]domain.WorkflowAttempt, 0, len(rows))
	for _, row := range rows {
		items = append(items, workflowAttemptFromGen(row))
	}
	return items, nil
}

// GetWorkflowAttempt reads one durable attempt, including its internal lease token.
func (s *Store) GetWorkflowAttempt(ctx context.Context, id domain.WorkflowAttemptID) (domain.WorkflowAttempt, bool, error) {
	row, err := s.qr.GetWorkflowAttempt(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowAttempt{}, false, nil
	}
	if err != nil {
		return domain.WorkflowAttempt{}, false, fmt.Errorf("get workflow attempt %s: %w", id, err)
	}
	return workflowAttemptFromGen(row), true, nil
}

type workflowIdempotencyInsert struct {
	Key         string
	WorkflowID  domain.WorkflowID
	Operation   domain.WorkflowOperation
	Fingerprint string
	ResultType  string
	ResultID    string
	CreatedAt   time.Time
}

func insertWorkflowIdempotency(ctx context.Context, q *gen.Queries, rec workflowIdempotencyInsert) error {
	rows, err := insertWorkflowIdempotencyRows(ctx, q, rec)
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrWorkflowIdempotencyConflict
	}
	return nil
}

func insertWorkflowIdempotencyRows(ctx context.Context, q *gen.Queries, rec workflowIdempotencyInsert) (int64, error) {
	return q.InsertWorkflowIdempotency(ctx, gen.InsertWorkflowIdempotencyParams{
		IdempotencyKey: rec.Key, WorkflowID: rec.WorkflowID, Operation: string(rec.Operation),
		RequestFingerprint: rec.Fingerprint, ResultType: rec.ResultType,
		ResultID: rec.ResultID, CreatedAt: rec.CreatedAt,
	})
}

func lookupWorkflowIdempotency(
	ctx context.Context,
	q *gen.Queries,
	key string,
	workflowID domain.WorkflowID,
	operation domain.WorkflowOperation,
	fingerprint string,
	resultType string,
) (gen.WorkflowIdempotency, bool, error) {
	row, err := q.GetWorkflowIdempotency(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return gen.WorkflowIdempotency{}, false, nil
	}
	if err != nil {
		return gen.WorkflowIdempotency{}, false, err
	}
	if row.Operation != string(operation) || row.RequestFingerprint != fingerprint ||
		row.ResultType != resultType || (workflowID != "" && row.WorkflowID != workflowID) {
		return row, true, domain.ErrWorkflowIdempotencyConflict
	}
	return row, true, nil
}

func (s *Store) lookupWorkflowIdempotency(
	ctx context.Context,
	key string,
	workflowID domain.WorkflowID,
	operation domain.WorkflowOperation,
	fingerprint string,
	resultType string,
) (gen.WorkflowIdempotency, bool, error) {
	return lookupWorkflowIdempotency(ctx, s.qr, key, workflowID, operation, fingerprint, resultType)
}

// utcTime and utcTimePtr normalize a caller-supplied instant to UTC before it
// reaches SQLite. modernc.org/sqlite binds time.Time as TEXT via t.String(), so
// every lease comparison in queries/workflow.sql is a LEXICOGRAPHIC string
// comparison; that ordering is only chronological while every stored value
// carries the same UTC offset. The Workflow service already passes .UTC(), but
// the store must not depend on its callers doing so.
func utcTime(t time.Time) time.Time {
	return t.UTC()
}

func utcTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	normalized := t.UTC()
	return &normalized
}

func workflowToInsert(rec domain.Workflow) gen.InsertWorkflowParams {
	return gen.InsertWorkflowParams{
		ID: rec.ID, Title: rec.Title, Goal: rec.Goal, OwnerSessionID: rec.OwnerSessionID,
		State: rec.State, Revision: rec.Revision,
		CreatedAt: utcTime(rec.CreatedAt), UpdatedAt: utcTime(rec.UpdatedAt),
	}
}

// workflowRepositoryToInsert canonicalizes path_ref so a direct store caller
// cannot persist two spellings of one repository (e.g. "a//b" and "a/b") and
// defeat UNIQUE (workflow_id, project_id, path_ref).
func workflowRepositoryToInsert(rec domain.WorkflowRepository) (gen.InsertWorkflowRepositoryParams, error) {
	pathRef, ok := domain.CanonicalWorkflowPath(rec.PathRef, true)
	if !ok {
		return gen.InsertWorkflowRepositoryParams{}, domain.ErrInvalidWorkflowRepository
	}
	return gen.InsertWorkflowRepositoryParams{
		WorkflowID: rec.WorkflowID, RepoKey: rec.RepoKey, ProjectID: rec.ProjectID,
		PathRef: pathRef, BaseSha: rec.BaseSHA, TargetRef: rec.TargetRef,
	}, nil
}

// workflowTaskRepositoryToInsert canonicalizes every allowed path for the same
// reason: the scope a worker receives must not depend on how the caller spelled
// it.
func workflowTaskRepositoryToInsert(rec domain.WorkflowTaskRepository) (gen.InsertWorkflowTaskRepositoryParams, error) {
	paths := make([]string, 0, len(rec.AllowedPaths))
	for _, allowedPath := range rec.AllowedPaths {
		canonical, ok := domain.CanonicalWorkflowPath(allowedPath, false)
		if !ok {
			return gen.InsertWorkflowTaskRepositoryParams{}, domain.ErrInvalidWorkflowTaskRepository
		}
		paths = append(paths, canonical)
	}
	allowedPaths, err := json.Marshal(paths)
	if err != nil {
		return gen.InsertWorkflowTaskRepositoryParams{}, fmt.Errorf(
			"encode allowed paths for task %s: %w", rec.TaskID, err,
		)
	}
	return gen.InsertWorkflowTaskRepositoryParams{
		WorkflowID: rec.WorkflowID, TaskID: rec.TaskID, RepoKey: rec.RepoKey,
		AllowedPathsJson: string(allowedPaths), BaseSha: rec.BaseSHA,
	}, nil
}

func workflowTaskToInsert(rec domain.WorkflowTask) gen.InsertWorkflowTaskParams {
	return gen.InsertWorkflowTaskParams{
		ID: rec.ID, WorkflowID: rec.WorkflowID, Kind: rec.Kind, Title: rec.Title,
		Brief: rec.Brief, State: rec.State, Priority: rec.Priority,
		AgentHarness: rec.AgentHarness, MaxAttempts: rec.MaxAttempts, Revision: rec.Revision,
		Model: rec.Model, Effort: rec.Effort, Profile: rec.Profile, Planner: rec.Planner,
		CreatedAt: utcTime(rec.CreatedAt), UpdatedAt: utcTime(rec.UpdatedAt),
	}
}

func workflowAttemptToInsert(rec domain.WorkflowAttempt) gen.InsertWorkflowAttemptParams {
	return gen.InsertWorkflowAttemptParams{
		ID: rec.ID, WorkflowID: rec.WorkflowID, TaskID: rec.TaskID, AttemptNo: rec.AttemptNo,
		SessionID: rec.SessionID, WorkerID: rec.WorkerID, LeaseToken: rec.LeaseToken,
		LeaseUntil:  timePtrToNullTime(utcTimePtr(rec.LeaseUntil)),
		HeartbeatAt: timePtrToNullTime(utcTimePtr(rec.HeartbeatAt)),
		State:       rec.State, StartedAt: timePtrToNullTime(utcTimePtr(rec.StartedAt)),
		EndedAt:   timePtrToNullTime(utcTimePtr(rec.EndedAt)),
		ErrorCode: rec.ErrorCode, CreatedAt: utcTime(rec.CreatedAt), UpdatedAt: utcTime(rec.UpdatedAt),
	}
}

func workflowFromGen(row gen.Workflow) domain.Workflow {
	return domain.Workflow{
		ID: row.ID, Title: row.Title, Goal: row.Goal, OwnerSessionID: row.OwnerSessionID,
		State: row.State, Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func workflowTaskFromGen(row gen.WorkflowTask) domain.WorkflowTask {
	return domain.WorkflowTask{
		ID: row.ID, WorkflowID: row.WorkflowID, Kind: row.Kind, Title: row.Title,
		Brief: row.Brief, State: row.State, Priority: row.Priority,
		AgentHarness: row.AgentHarness, MaxAttempts: row.MaxAttempts, Revision: row.Revision,
		Model: row.Model, Effort: row.Effort, Profile: row.Profile, Planner: row.Planner,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func workflowRepositoryFromGen(row gen.WorkflowRepository) domain.WorkflowRepository {
	return domain.WorkflowRepository{
		WorkflowID: row.WorkflowID, RepoKey: row.RepoKey, ProjectID: row.ProjectID,
		PathRef: row.PathRef, BaseSHA: row.BaseSha, TargetRef: row.TargetRef,
	}
}

func workflowAttemptFromGen(row gen.WorkflowAttempt) domain.WorkflowAttempt {
	return domain.WorkflowAttempt{
		ID: row.ID, WorkflowID: row.WorkflowID, TaskID: row.TaskID, AttemptNo: row.AttemptNo,
		SessionID: row.SessionID, WorkerID: row.WorkerID, LeaseToken: row.LeaseToken,
		LeaseUntil: nullTimeToTimePtr(row.LeaseUntil), HeartbeatAt: nullTimeToTimePtr(row.HeartbeatAt),
		State: row.State, StartedAt: nullTimeToTimePtr(row.StartedAt), EndedAt: nullTimeToTimePtr(row.EndedAt),
		ErrorCode: row.ErrorCode, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
