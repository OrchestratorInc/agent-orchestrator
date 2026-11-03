package controllers

import (
	"context"
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	workflowsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// workflowCreateBodyLimit caps a create body. A workflow graph is a whole DAG —
// repositories, tasks, edges, and per-task repository scopes — so it is far
// larger than an ordinary REST body and gets its own 1 MiB ceiling instead of
// the default reader limit.
const workflowCreateBodyLimit = 1 << 20

// WorkflowService defines the durable workflow operations exposed over HTTP.
// Every write takes its idempotency key as a body field, matching the rest of
// the API surface: no route reads an Idempotency-Key header.
type WorkflowService interface {
	Create(ctx context.Context, idempotencyKey string, input workflowsvc.CreateInput) (domain.Workflow, bool, error)
	Get(ctx context.Context, id domain.WorkflowID) (domain.Workflow, error)
	GetDefinition(ctx context.Context, id domain.WorkflowID) (workflowsvc.Definition, error)
	List(ctx context.Context, state domain.WorkflowState, limit, offset int64) ([]domain.Workflow, error)
	ListTasks(ctx context.Context, workflowID domain.WorkflowID, state domain.WorkflowTaskState, limit, offset int64) ([]domain.WorkflowTask, error)
	GetTask(ctx context.Context, workflowID domain.WorkflowID, taskID domain.WorkflowTaskID) (domain.WorkflowTask, error)
	ListAttempts(ctx context.Context, workflowID domain.WorkflowID, taskID domain.WorkflowTaskID) ([]domain.WorkflowAttempt, error)
	Activate(ctx context.Context, workflowID domain.WorkflowID, expectedRevision int64, idempotencyKey string) (domain.Workflow, bool, error)
	Claim(ctx context.Context, input workflowsvc.ClaimInput) (workflowsvc.ClaimResult, bool, error)
	Start(ctx context.Context, input workflowsvc.StartInput) (domain.WorkflowAttempt, bool, error)
	Settle(ctx context.Context, input workflowsvc.SettleInput) (domain.WorkflowAttempt, bool, error)
	Retry(ctx context.Context, input workflowsvc.RetryInput) (domain.WorkflowTask, bool, error)
	RecoverExpired(ctx context.Context) (int64, error)
}

// WorkflowsController serves the durable workflow API routes.
type WorkflowsController struct{ Svc WorkflowService }

// Register mounts the workflow API routes on r.
func (c *WorkflowsController) Register(r chi.Router) {
	r.Get("/workflows", c.list)
	r.Post("/workflows", c.create)
	r.Post("/workflows/recover-expired", c.recoverExpired)
	r.Get("/workflows/{workflowId}", c.definition)
	r.Get("/workflows/{workflowId}/tasks", c.listTasks)
	r.Get("/workflows/{workflowId}/tasks/{taskId}", c.task)
	r.Post("/workflows/{workflowId}/activate", c.activate)
	r.Post("/workflows/{workflowId}/tasks/{taskId}/claim", c.claim)
	r.Post("/workflows/{workflowId}/attempts/{attemptId}/start", c.start)
	r.Post("/workflows/{workflowId}/attempts/{attemptId}/settle", c.settle)
	r.Post("/workflows/{workflowId}/tasks/{taskId}/retry", c.retry)
}

// create persists a complete workflow DAG. It answers 201 for a fresh insert
// and 200 when the idempotency key replays the original request, so a caller
// that retried after a lost response can tell the two apart.
func (c *WorkflowsController) create(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, workflowCreateBodyLimit)
	var req CreateWorkflowRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	rec, applied, err := c.Svc.Create(r.Context(), req.IdempotencyKey, workflowCreateInput(req))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	status := http.StatusOK
	if applied {
		status = http.StatusCreated
	}
	envelope.WriteJSON(w, status, WorkflowEnvelope{Workflow: workflowResponse(rec)})
}

// definition returns one workflow with its complete persisted DAG.
func (c *WorkflowsController) definition(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/workflows/{workflowId}")
		return
	}
	definition, err := c.Svc.GetDefinition(r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workflowDefinitionResponse(definition))
}

// list returns one page of workflows, optionally filtered to one state.
func (c *WorkflowsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/workflows")
		return
	}
	var state domain.WorkflowState
	if value := r.URL.Query().Get("state"); value != "" {
		state = domain.WorkflowState(value)
		if !state.Valid() {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "Unknown workflow state", nil)
			return
		}
	}
	limit, offset, code, message := workflowPage(r)
	if code != "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", code, message, nil)
		return
	}
	items, err := c.Svc.List(r.Context(), state, limit, offset)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	workflows := make([]WorkflowResponse, 0, len(items))
	for _, rec := range items {
		workflows = append(workflows, workflowResponse(rec))
	}
	envelope.WriteJSON(w, http.StatusOK, ListWorkflowsResponse{
		Workflows:  workflows,
		NextCursor: workflowNextCursor(offset, len(items), limit),
	})
}

// listTasks returns one page of a workflow's tasks, optionally filtered to one state.
func (c *WorkflowsController) listTasks(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/workflows/{workflowId}/tasks")
		return
	}
	var state domain.WorkflowTaskState
	if value := r.URL.Query().Get("state"); value != "" {
		state = domain.WorkflowTaskState(value)
		if !state.Valid() {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "Unknown workflow task state", nil)
			return
		}
	}
	limit, offset, code, message := workflowPage(r)
	if code != "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", code, message, nil)
		return
	}
	items, err := c.Svc.ListTasks(r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")), state, limit, offset)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	tasks := make([]WorkflowTaskResponse, 0, len(items))
	for _, task := range items {
		tasks = append(tasks, workflowTaskResponse(task))
	}
	envelope.WriteJSON(w, http.StatusOK, ListWorkflowTasksResponse{
		Tasks:      tasks,
		NextCursor: workflowNextCursor(offset, len(items), limit),
	})
}

// task returns one task with its durable attempt history.
func (c *WorkflowsController) task(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/workflows/{workflowId}/tasks/{taskId}")
		return
	}
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	taskID := domain.WorkflowTaskID(chi.URLParam(r, "taskId"))
	task, err := c.Svc.GetTask(r.Context(), workflowID, taskID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	attempts, err := c.Svc.ListAttempts(r.Context(), workflowID, taskID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowTaskEnvelope{
		Task:     workflowTaskResponse(task),
		Attempts: workflowAttemptResponses(attempts),
	})
}

// activate moves a draft workflow to active, guarded by its revision.
func (c *WorkflowsController) activate(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/activate")
		return
	}
	var req ActivateWorkflowRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	rec, _, err := c.Svc.Activate(r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")), req.ExpectedRevision, req.IdempotencyKey)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowEnvelope{Workflow: workflowResponse(rec)})
}

// claim leases a ready task to a worker and answers 201 with the one-time
// lease capability, or 200 when the idempotency key replays the first claim.
func (c *WorkflowsController) claim(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/tasks/{taskId}/claim")
		return
	}
	var req ClaimWorkflowTaskRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	result, applied, err := c.Svc.Claim(r.Context(), workflowsvc.ClaimInput{
		WorkflowID:       domain.WorkflowID(chi.URLParam(r, "workflowId")),
		TaskID:           domain.WorkflowTaskID(chi.URLParam(r, "taskId")),
		ExpectedRevision: req.ExpectedRevision,
		WorkerID:         req.WorkerID,
		IdempotencyKey:   req.IdempotencyKey,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	status := http.StatusOK
	if applied {
		status = http.StatusCreated
	}
	envelope.WriteJSON(w, status, ClaimWorkflowTaskResponse{
		Attempt:    workflowAttemptResponse(result.Attempt),
		LeaseToken: result.LeaseToken,
	})
}

// start marks a claimed attempt as running after checking its lease capability.
func (c *WorkflowsController) start(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/start")
		return
	}
	var req StartWorkflowAttemptRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	attempt, _, err := c.Svc.Start(r.Context(), workflowsvc.StartInput{
		WorkflowID:     domain.WorkflowID(chi.URLParam(r, "workflowId")),
		AttemptID:      domain.WorkflowAttemptID(chi.URLParam(r, "attemptId")),
		LeaseToken:     req.LeaseToken,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowAttemptEnvelope{Attempt: workflowAttemptResponse(attempt)})
}

// settle records a worker's terminal result for its attempt.
func (c *WorkflowsController) settle(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/settle")
		return
	}
	var req SettleWorkflowAttemptRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	attempt, _, err := c.Svc.Settle(r.Context(), workflowsvc.SettleInput{
		WorkflowID:     domain.WorkflowID(chi.URLParam(r, "workflowId")),
		AttemptID:      domain.WorkflowAttemptID(chi.URLParam(r, "attemptId")),
		LeaseToken:     req.LeaseToken,
		Result:         domain.WorkflowAttemptState(req.Result),
		ErrorCode:      req.ErrorCode,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowAttemptEnvelope{Attempt: workflowAttemptResponse(attempt)})
}

// retry reopens a retry_wait task after a failed attempt.
func (c *WorkflowsController) retry(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/tasks/{taskId}/retry")
		return
	}
	var req RetryWorkflowTaskRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	task, _, err := c.Svc.Retry(r.Context(), workflowsvc.RetryInput{
		WorkflowID:       domain.WorkflowID(chi.URLParam(r, "workflowId")),
		TaskID:           domain.WorkflowTaskID(chi.URLParam(r, "taskId")),
		ExpectedRevision: req.ExpectedRevision,
		IdempotencyKey:   req.IdempotencyKey,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	attempts, err := c.Svc.ListAttempts(r.Context(), task.WorkflowID, task.ID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowTaskEnvelope{
		Task:     workflowTaskResponse(task),
		Attempts: workflowAttemptResponses(attempts),
	})
}

// recoverExpired sweeps attempts whose lease expired, moving them to unknown
// and blocking their tasks. It takes no body.
func (c *WorkflowsController) recoverExpired(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/recover-expired")
		return
	}
	recovered, err := c.Svc.RecoverExpired(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RecoverExpiredWorkflowsResponse{Recovered: recovered})
}

func workflowCreateInput(req CreateWorkflowRequest) workflowsvc.CreateInput {
	input := workflowsvc.CreateInput{
		Title: req.Title,
		Goal:  req.Goal,
	}
	if req.OwnerSessionID != "" {
		owner := domain.SessionID(req.OwnerSessionID)
		input.OwnerSessionID = &owner
	}
	input.Repositories = make([]workflowsvc.RepositoryInput, 0, len(req.Repositories))
	for _, repository := range req.Repositories {
		input.Repositories = append(input.Repositories, workflowsvc.RepositoryInput{
			RepoKey:   domain.WorkflowRepoKey(repository.RepoKey),
			ProjectID: domain.ProjectID(repository.ProjectID),
			PathRef:   repository.PathRef,
			BaseSHA:   repository.BaseSHA,
			TargetRef: repository.TargetRef,
		})
	}
	input.Tasks = make([]workflowsvc.TaskInput, 0, len(req.Tasks))
	for _, task := range req.Tasks {
		input.Tasks = append(input.Tasks, workflowsvc.TaskInput{
			ID:           domain.WorkflowTaskID(task.ID),
			Kind:         domain.WorkflowTaskKind(task.Kind),
			Title:        task.Title,
			Brief:        task.Brief,
			Priority:     task.Priority,
			AgentHarness: domain.AgentHarness(task.AgentHarness),
			MaxAttempts:  task.MaxAttempts,
		})
	}
	input.Dependencies = make([]workflowsvc.DependencyInput, 0, len(req.Dependencies))
	for _, dependency := range req.Dependencies {
		input.Dependencies = append(input.Dependencies, workflowsvc.DependencyInput{
			FromTaskID: domain.WorkflowTaskID(dependency.FromTaskID),
			ToTaskID:   domain.WorkflowTaskID(dependency.ToTaskID),
		})
	}
	input.TaskRepositories = make([]workflowsvc.TaskRepositoryInput, 0, len(req.TaskRepositories))
	for _, binding := range req.TaskRepositories {
		input.TaskRepositories = append(input.TaskRepositories, workflowsvc.TaskRepositoryInput{
			TaskID:       domain.WorkflowTaskID(binding.TaskID),
			RepoKey:      domain.WorkflowRepoKey(binding.RepoKey),
			AllowedPaths: binding.AllowedPaths,
			BaseSHA:      binding.BaseSHA,
		})
	}
	return input
}

func workflowResponse(rec domain.Workflow) WorkflowResponse {
	response := WorkflowResponse{
		ID: string(rec.ID), Title: rec.Title, Goal: rec.Goal, State: string(rec.State),
		Revision: rec.Revision, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
	}
	if rec.OwnerSessionID != nil {
		response.OwnerSessionID = string(*rec.OwnerSessionID)
	}
	return response
}

func workflowRepositoryResponses(items []domain.WorkflowRepository) []WorkflowRepositoryResponse {
	responses := make([]WorkflowRepositoryResponse, 0, len(items))
	for _, repository := range items {
		responses = append(responses, WorkflowRepositoryResponse{
			RepoKey: string(repository.RepoKey), ProjectID: string(repository.ProjectID),
			PathRef: repository.PathRef, BaseSHA: repository.BaseSHA, TargetRef: repository.TargetRef,
		})
	}
	return responses
}

func workflowTaskResponse(task domain.WorkflowTask) WorkflowTaskResponse {
	return WorkflowTaskResponse{
		ID: string(task.ID), WorkflowID: string(task.WorkflowID), Kind: string(task.Kind),
		Title: task.Title, Brief: task.Brief, State: string(task.State), Priority: task.Priority,
		AgentHarness: string(task.AgentHarness), MaxAttempts: task.MaxAttempts,
		Revision: task.Revision, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
}

func workflowEdgeResponses(items []domain.WorkflowEdge) []WorkflowEdgeResponse {
	responses := make([]WorkflowEdgeResponse, 0, len(items))
	for _, edge := range items {
		responses = append(responses, WorkflowEdgeResponse{
			FromTaskID: string(edge.FromTaskID), ToTaskID: string(edge.ToTaskID),
		})
	}
	return responses
}

func workflowTaskRepositoryResponses(items []domain.WorkflowTaskRepository) []WorkflowTaskRepositoryResponse {
	responses := make([]WorkflowTaskRepositoryResponse, 0, len(items))
	for _, binding := range items {
		allowed := binding.AllowedPaths
		if allowed == nil {
			allowed = []string{}
		}
		responses = append(responses, WorkflowTaskRepositoryResponse{
			TaskID: string(binding.TaskID), RepoKey: string(binding.RepoKey),
			AllowedPaths: allowed, BaseSHA: binding.BaseSHA,
		})
	}
	return responses
}

// workflowAttemptResponse maps one attempt. domain.WorkflowAttempt marshals its
// LeaseToken as `json:"-"`, and this projection has no field for it at all, so
// an ordinary attempt response can never leak the capability: only the claim
// response carries it, as its own top-level field.
func workflowAttemptResponse(attempt domain.WorkflowAttempt) WorkflowAttemptResponse {
	response := WorkflowAttemptResponse{
		ID: string(attempt.ID), WorkflowID: string(attempt.WorkflowID), TaskID: string(attempt.TaskID),
		AttemptNo: attempt.AttemptNo, WorkerID: attempt.WorkerID, LeaseUntil: attempt.LeaseUntil,
		HeartbeatAt: attempt.HeartbeatAt, State: string(attempt.State), StartedAt: attempt.StartedAt,
		EndedAt: attempt.EndedAt, ErrorCode: attempt.ErrorCode,
		CreatedAt: attempt.CreatedAt, UpdatedAt: attempt.UpdatedAt,
	}
	if attempt.SessionID != nil {
		response.SessionID = string(*attempt.SessionID)
	}
	return response
}

func workflowAttemptResponses(items []domain.WorkflowAttempt) []WorkflowAttemptResponse {
	responses := make([]WorkflowAttemptResponse, 0, len(items))
	for _, attempt := range items {
		responses = append(responses, workflowAttemptResponse(attempt))
	}
	return responses
}

func workflowDefinitionResponse(definition workflowsvc.Definition) WorkflowDefinitionResponse {
	return WorkflowDefinitionResponse{
		Workflow:         workflowResponse(definition.Workflow),
		Repositories:     workflowRepositoryResponses(definition.Repositories),
		Tasks:            workflowTaskResponses(definition.Tasks),
		Edges:            workflowEdgeResponses(definition.Edges),
		TaskRepositories: workflowTaskRepositoryResponses(definition.TaskRepositories),
	}
}

func workflowTaskResponses(items []domain.WorkflowTask) []WorkflowTaskResponse {
	responses := make([]WorkflowTaskResponse, 0, len(items))
	for _, task := range items {
		responses = append(responses, workflowTaskResponse(task))
	}
	return responses
}

// workflowPage decodes the shared limit/cursor pagination pair. The cursor is
// the base64 of a numeric offset, matching automationPage.
//
// The two rejection kinds are deliberately distinct because they mean different
// things to a client: an unparseable limit or cursor is a malformed query
// (INVALID_QUERY), while a well-formed limit outside 1..100 is the pagination
// bound the workflow service itself enforces, reported with its code
// (INVALID_PAGINATION). limit=0 is the service's "use the default" spelling, so
// it is accepted like an absent limit and pages 50 at a time.
func workflowPage(r *http.Request) (limit, offset int64, code, message string) {
	limit = 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, 0, "INVALID_QUERY", "limit must be an integer"
		}
		if parsed != 0 {
			if parsed < 1 || parsed > 100 {
				return 0, 0, "INVALID_PAGINATION", "Limit must be between 1 and 100 and offset cannot be negative"
			}
			limit = parsed
		}
	}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return 0, 0, "INVALID_QUERY", "cursor is invalid"
		}
		parsed, err := strconv.ParseInt(string(decoded), 10, 64)
		if err != nil || parsed < 0 {
			return 0, 0, "INVALID_QUERY", "cursor is invalid"
		}
		offset = parsed
	}
	return limit, offset, "", ""
}

// workflowNextCursor emits a cursor only when the page came back exactly full.
// The service returns a bare slice with no total, so a short page is the only
// proof that nothing follows; a full page may or may not have more, and asking
// again with the returned cursor settles it.
func workflowNextCursor(offset int64, returned int, limit int64) string {
	if int64(returned) != limit {
		return ""
	}
	return encodeWorkflowCursor(offset + int64(returned))
}

func encodeWorkflowCursor(offset int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(offset, 10)))
}
