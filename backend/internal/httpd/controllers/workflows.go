package controllers

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	collaborationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/collaboration"
	executionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	interventionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
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
	// Resolve records an operator's decided outcome for an attempt whose lease expired. It is
	// the only exit from UNKNOWN and it never re-dispatches work.
	Resolve(ctx context.Context, input workflowsvc.ResolveInput) (domain.WorkflowAttempt, bool, error)
}

// WorkflowsController serves the durable workflow API routes.
//
// The M2 fields are separate slots so a deployment can wire the M1 surface alone: nil leaves
// only the corresponding M2 routes answering 501, exactly like a nil Svc does for M1.
type WorkflowsController struct {
	Svc              WorkflowService
	ExecutionPermits ExecutionPermitReader
	ExecutionFence   func(context.Context) (domain.SchedulerFence, error)
	// Now 与执行服务共用时间源，避免许可校验和能力过期判断混用时钟；默认使用真实时间。
	Now              func() time.Time
	RetrySchedule    func(context.Context, domain.WorkflowID, domain.WorkflowTaskID, int64) error
	Materializer     TaskRequestMaterializer
	Materializations TaskRequestMaterializationReader
	Recovery         WorkflowRecovery
	// Capabilities reports graded capability evidence.
	Capabilities CapabilityResolver
	// Bindings reads the persisted binding projection.
	Bindings BindingReader
	// Binder creates or resumes a binding intent.
	Binder AttemptBinder
	// Interventions dispatches Steer, Queue, and Stop.
	Interventions InterventionCoordinator
	// Collaboration stores messages and task requests.
	Collaboration CollaborationCoordinator
	// Ownership resolves the durable Task/Project/Repo facts a bind must carry. Nil leaves the
	// bind route answering 501 rather than binding with facts the request body supplied.
	Ownership OwnershipResolver
	// Principals derives the trusted caller for the M2 routes. Nil refuses every M2 request:
	// an unwired identity check is not permission.
	Principals *principal.Resolver
	// Audit appends sensitive operator decisions (resolve, decide, bind, reconcile, resume) to
	// the audit trail. A nil recorder is the "no durable audit table yet" configuration: decisions
	// still work, and each one is logged, because refusing every operator action for want of an
	// audit store would be a worse failure than an unrecorded one. It is never silently a no-op
	// when wired: a recorder error fails the request.
	Audit principal.AuditRecorder
}

// Register mounts the workflow API routes on r.
func (c *WorkflowsController) Register(r chi.Router) {
	r.Post("/workflows/plan", c.startPlan)
	r.Post("/workflows/{workflowId}/replan", c.startReplan)
	r.Post("/workflows/{workflowId}/plan-candidates", c.preparePlan)
	r.Get("/workflows/{workflowId}/plan-candidates", c.listPlans)
	r.Post("/workflows/{workflowId}/plan-candidates/{candidateId}/confirm", c.confirmPlan)
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
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
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
	if !c.recordMutation(w, r, caller, rec.ID, "", req.IdempotencyKey, "create_workflow", applied) {
		return
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
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	rec, applied, err := c.Svc.Activate(r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")), req.ExpectedRevision, req.IdempotencyKey)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !c.recordMutation(w, r, caller, rec.ID, "", req.IdempotencyKey, "activate_workflow", applied) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowEnvelope{Workflow: workflowResponse(rec)})
}

// claim 经独立 Operator 批准领取任务；内部租约不会进入响应。
func (c *WorkflowsController) claim(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/tasks/{taskId}/claim")
		return
	}
	var req ClaimWorkflowTaskRequest
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
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
	if !c.recordMutation(w, r, caller, result.Attempt.WorkflowID, result.Attempt.ID, req.IdempotencyKey, "claim_task", applied) {
		return
	}
	envelope.WriteJSON(w, status, ClaimWorkflowTaskResponse{Attempt: workflowAttemptResponse(result.Attempt)})
}

// start 消费 Operator 批准后读取内部许可，Store 仍验证租约与任期 CAS。
func (c *WorkflowsController) start(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/start")
		return
	}
	var req StartWorkflowAttemptRequest
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	permit, ok := c.executionPermit(w, r)
	if !ok {
		return
	}
	attempt, applied, err := c.Svc.Start(r.Context(), workflowsvc.StartInput{
		WorkflowID:     domain.WorkflowID(chi.URLParam(r, "workflowId")),
		AttemptID:      domain.WorkflowAttemptID(chi.URLParam(r, "attemptId")),
		LeaseToken:     permit.LeaseToken,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !c.recordMutation(w, r, caller, attempt.WorkflowID, attempt.ID, req.IdempotencyKey, "start_attempt", applied) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowAttemptEnvelope{Attempt: workflowAttemptResponse(attempt)})
}

// settle 的外部调用只接受 Operator 批准，凭据从服务端当前许可读取。
func (c *WorkflowsController) settle(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/settle")
		return
	}
	var req SettleWorkflowAttemptRequest
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	permit, ok := c.executionPermit(w, r)
	if !ok {
		return
	}
	attempt, applied, err := c.Svc.Settle(r.Context(), workflowsvc.SettleInput{
		WorkflowID:     domain.WorkflowID(chi.URLParam(r, "workflowId")),
		AttemptID:      domain.WorkflowAttemptID(chi.URLParam(r, "attemptId")),
		LeaseToken:     permit.LeaseToken,
		Result:         domain.WorkflowAttemptState(req.Result),
		ErrorCode:      req.ErrorCode,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !c.recordMutation(w, r, caller, attempt.WorkflowID, attempt.ID, req.IdempotencyKey, "settle_attempt", applied) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowAttemptEnvelope{Attempt: workflowAttemptResponse(attempt)})
}

// retry reopens a retry_wait task after a failed attempt.
func (c *WorkflowsController) retry(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil || c.RetrySchedule == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/tasks/{taskId}/retry")
		return
	}
	var req RetryWorkflowTaskRequest
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	if err := c.RetrySchedule(r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")), domain.WorkflowTaskID(chi.URLParam(r, "taskId")), req.ExpectedRevision); err != nil {
		envelope.WriteError(w, r, schedulerError(err))
		return
	}
	task, applied, err := c.Svc.Retry(r.Context(), workflowsvc.RetryInput{
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
	if !c.recordMutation(w, r, caller, task.WorkflowID, "", req.IdempotencyKey, "retry_task", applied) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowTaskEnvelope{
		Task:     workflowTaskResponse(task),
		Attempts: workflowAttemptResponses(attempts),
	})
}

// recoverExpired 将全局授权和恢复结果交给同一 Store 事务记录，不伪造 Workflow 审计归属。
func (c *WorkflowsController) recoverExpired(w http.ResponseWriter, r *http.Request) {
	if c.Recovery == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/recover-expired")
		return
	}
	var req RecoverExpiredWorkflowsRequest
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	if req.Reason == "" {
		req.Reason = "OPERATOR_RECOVERY"
	}
	recovered, _, err := c.Recovery.RecoverExpiredWorkflowsAuthorized(r.Context(), policyChange(r, req.IdempotencyKey, req.Reason, caller))
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
			Model:        task.Model, Effort: task.Effort, Profile: task.Profile,
			MaxAttempts: task.MaxAttempts,
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
		Model: task.Model, Effort: task.Effort, Profile: task.Profile, Planner: task.Planner,
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

// --- M2 capability, binding, intervention, and collaboration handlers -----------
//
// These routes extend the existing /workflows family rather than introducing a second entry
// point, and they reuse the same envelope, request id, and 501-when-unwired behaviour as the
// M1 routes above.
//
// Three of them exist to stop a 200 from implying more than the service proved:
//
//   - a refused steer answers with queueAvailable, not with "steered";
//   - a spawn whose outcome is in flight answers spawn_in_progress, not a 200 with an empty
//     binding;
//   - an approved task request that could not be materialized is NOT a success, because the
//     workflow service exposes no way to add a task.

// CapabilityResolver is the M2 capability read surface.
type CapabilityResolver interface {
	Resolve(ctx context.Context, request ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error)
}

// AttemptBinder is the M2 bind surface. It corresponds to execution.Coordinator.BindAttempt
// without this package depending on the coordinator's concrete type.
//
// This is the WIRED contract, and it is deliberately the small one: *execution.Coordinator
// satisfies exactly these three methods today, so wiring it needs no adapter and no change in
// the daemon. Resume and the liveness check are OPTIONAL capabilities (AttemptResumer,
// AttemptLiveTarget below), discovered per request, so a coordinator that grows them lights up
// the extra routes and fields with no controller change — and a partial deployment answers 501
// instead of failing to compile.
type AttemptBinder interface {
	BindAttempt(
		ctx context.Context,
		request ports.SpawnRequest,
		repositories []domain.WorkflowAttemptRepoBinding,
	) (executionsvc.BindOutcome, error)
	ObserveSession(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) (ports.SessionObservation, domain.WorkflowBindingState, error)
	Reconcile(ctx context.Context, attemptID domain.WorkflowAttemptID) (executionsvc.BindOutcome, error)
}

// AttemptResumer is the optional restart recovery capability.
//
// *execution.Coordinator gains it when execution.Coordinator.Resume lands; until then the resume
// route answers 501 rather than pretending a recovery happened. It never re-spawns: resume
// re-attaches to an existing session, which is why the response can report Spawned=false and
// still be a success.
type AttemptResumer interface {
	Resume(ctx context.Context, attemptID domain.WorkflowAttemptID) (executionsvc.BindOutcome, error)
}

// AttemptLiveTarget is the optional liveness check.
//
// The five-return shape carries the coordinator's own reason string, so the controller never
// re-derives WHY a binding is not live — that judgement belongs to the package that owns the
// rule. A coordinator exposing only the four-return form leaves live/reasonCode absent from the
// observe response, which a client must read as "not checked", never as "dead".
type AttemptLiveTarget interface {
	LiveTarget(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) (domain.WorkflowAttemptBinding, ports.SessionObservation, bool, string, error)
}

// AttemptOwnership is the durable Task/Project/Repo facts a bind must carry.
//
// It mirrors execution.AttemptOwnership field for field, but lives here so this package does not
// depend on service/execution's ownership surface: the daemon adapts one to the other, which
// keeps the dependency pointing at the wire contract rather than inward at an implementation.
type AttemptOwnership struct {
	Attempt   domain.WorkflowAttempt
	Task      domain.WorkflowTask
	Repos     []domain.WorkflowAttemptRepoBinding
	ProjectID domain.ProjectID
	Harness   domain.AgentHarness
}

// OwnershipResolver resolves the durable facts for one attempt.
//
// Every fact a bind sends comes from here, never from the request body, so a caller cannot bind
// an attempt to a project or repository it was not scoped to.
type OwnershipResolver interface {
	ResolveAttemptOwnership(
		ctx context.Context,
		workflowID domain.WorkflowID,
		attemptID domain.WorkflowAttemptID,
	) (AttemptOwnership, error)
}

// InterventionCoordinator is the M2 Steer/Queue/Stop surface.
type InterventionCoordinator interface {
	Steer(ctx context.Context, request interventionsvc.SteerRequest) (interventionsvc.Outcome, error)
	Queue(ctx context.Context, request interventionsvc.QueueRequest) (interventionsvc.Outcome, error)
	Stop(
		ctx context.Context,
		request interventionsvc.StopRequest,
	) (interventionsvc.Outcome, *domain.ReplanRequest, error)
}

// CollaborationCoordinator is the M2 message/request surface.
type CollaborationCoordinator interface {
	CreateMessage(
		ctx context.Context,
		input domain.CreateWorkflowMessageInput,
	) (collaborationsvc.MessageOutcome, error)
	ListMessages(
		ctx context.Context,
		workflowID domain.WorkflowID,
		limit, offset int64,
	) ([]domain.WorkflowMessage, error)
	CreateTaskRequest(
		ctx context.Context,
		input collaborationsvc.CreateTaskRequestInput,
	) (collaborationsvc.TaskRequestOutcome, error)
	Decide(
		ctx context.Context,
		input collaborationsvc.DecideInput,
	) (domain.WorkflowTaskRequestDecision, error)
	ListTaskRequests(
		ctx context.Context,
		workflowID domain.WorkflowID,
		limit, offset int64,
	) ([]domain.WorkflowTaskRequest, error)
	// GetTaskRequest reads one request inside the workflow. The workflow id is part of the
	// lookup, so a request id from another workflow is simply not found.
	GetTaskRequest(
		ctx context.Context,
		workflowID domain.WorkflowID,
		requestID string,
	) (domain.WorkflowTaskRequest, error)
}

// BindingReader reads the persisted binding projection for the read-only route.
type BindingReader interface {
	GetWorkflowAttemptBinding(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) (domain.WorkflowAttemptBinding, bool, error)
	ListWorkflowAttemptRepoBindings(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) ([]domain.WorkflowAttemptRepoBinding, error)
}

// ExecutionPermitReader 在 Operator 批准后读取内部租约快照，客户端永远不能指定 lease。
type ExecutionPermitReader interface {
	LoadExecutionPermit(context.Context, domain.WorkflowAttemptID, domain.SchedulerFence, time.Time) (domain.ExecutionPermit, error)
}

// TaskRequestMaterializer 消费已批准原件并在单一事务扩展图。
type TaskRequestMaterializer interface {
	MaterializeTaskRequest(context.Context, domain.TaskRequestMaterializationInput, principal.Caller) (domain.TaskRequestMaterialization, bool, error)
}

// TaskRequestMaterializationReader 让 Decide 重放报告真实物化状态。
type TaskRequestMaterializationReader interface {
	GetWorkflowTaskMaterialization(context.Context, domain.WorkflowID, string) (domain.TaskRequestMaterialization, bool, error)
}

// WorkflowRecovery 将全局人工恢复的幂等键、批准指纹与结果一起持久化。
type WorkflowRecovery interface {
	RecoverExpiredWorkflowsAuthorized(context.Context, domain.PolicyChange) (int64, bool, error)
}

// operatorWrite 先完整解析原始正文，再消费只属于这一次具体操作的 Operator 批准。
func (c *WorkflowsController) operatorWrite(w http.ResponseWriter, r *http.Request, body any) (principal.Caller, bool) {
	bound, ok := withApprovalDigest(w, r)
	if !ok {
		return principal.Caller{}, false
	}
	*r = *bound
	if err := decodeJSONStrict(r, body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return principal.Caller{}, false
	}
	caller, ok := c.caller(w, r, principal.RequestFacts{WorkflowID: domain.WorkflowID(chi.URLParam(r, "workflowId"))})
	if !ok {
		return principal.Caller{}, false
	}
	if !caller.IsOperator() || caller.IsAgent() || caller.AttemptID != "" || caller.LeaseProven || caller.OperatorTokenFingerprint == "" {
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED", "This operation requires a human operator approval"))
		return principal.Caller{}, false
	}
	return caller, true
}

// now 保持 HTTP 的时间判断与注入的执行服务时钟一致，生产未注入时读取当前 UTC 时间。
func (c *WorkflowsController) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// executionPermit 只使用当前 Scheduler 身份读取服务端许可，跨 Workflow 或旧任期均不执行。
func (c *WorkflowsController) executionPermit(w http.ResponseWriter, r *http.Request) (domain.ExecutionPermit, bool) {
	if c.ExecutionPermits == nil || c.ExecutionFence == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return domain.ExecutionPermit{}, false
	}
	fence, err := c.ExecutionFence(r.Context())
	if err != nil {
		envelope.WriteError(w, r, schedulerError(err))
		return domain.ExecutionPermit{}, false
	}
	permit, err := c.ExecutionPermits.LoadExecutionPermit(r.Context(), domain.WorkflowAttemptID(chi.URLParam(r, "attemptId")), fence, c.now())
	if err != nil {
		envelope.WriteError(w, r, schedulerError(err))
		return domain.ExecutionPermit{}, false
	}
	if permit.WorkflowID != domain.WorkflowID(chi.URLParam(r, "workflowId")) || permit.AttemptID != domain.WorkflowAttemptID(chi.URLParam(r, "attemptId")) {
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED", "The execution permit does not belong to this workflow"))
		return domain.ExecutionPermit{}, false
	}
	*r = *r.WithContext(ports.WithExecutionPermit(r.Context(), permit))
	return permit, true
}

// recordMutation 为已成功的 M1 授权保留审计，包括幂等重放；空 Workflow 的全局操作另走事务账本。
func (c *WorkflowsController) recordMutation(w http.ResponseWriter, r *http.Request, caller principal.Caller, workflowID domain.WorkflowID, attemptID domain.WorkflowAttemptID, key, operation string, applied bool) bool {
	outcome := "replayed"
	if applied {
		outcome = "applied"
	}
	if c.recordDecision(r, principal.AuditRecord{ActorKind: caller.Kind, ActorRef: caller.Ref(), WorkflowID: workflowID, AttemptID: attemptID, RequestID: key, Decision: operation, Outcome: outcome, TokenFingerprint: caller.OperatorTokenFingerprint}) {
		return true
	}
	envelope.WriteError(w, r, apierr.Internal("WORKFLOW_AUDIT_UNAVAILABLE", "The operation was applied but its audit record could not be stored"))
	return false
}

// RegisterM2 mounts the M2 workflow routes. It is separate from Register so the M1 route set
// stays a fixed, audited list.
//
// "/workflows/capabilities" is registered as a static segment. chi v5.1.0 matches node types in
// a fixed order with ntStatic before ntParam, so this cannot be shadowed by
// "/workflows/{workflowId}" whatever the registration order — and TestWorkflowStaticRoutesPrecede
// keeps that true, including for a static segment that looks like a valid workflow id.
func (c *WorkflowsController) RegisterM2(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(operatorApprovalBinding)
		r.Get("/workflows/capabilities", c.capabilities)
		r.Get("/workflows/{workflowId}/attempts/{attemptId}/binding", c.binding)
		r.Get("/workflows/{workflowId}/attempts/{attemptId}/capabilities", c.attemptCapabilities)
		r.Get("/workflows/{workflowId}/attempts/{attemptId}/interventions", c.interventionReceipts)
		r.Post("/workflows/{workflowId}/attempts/{attemptId}/bind", c.bind)
		r.Get("/workflows/{workflowId}/attempts/{attemptId}/observe", c.observe)
		r.Post("/workflows/{workflowId}/attempts/{attemptId}/reconcile", c.reconcile)
		r.Post("/workflows/{workflowId}/attempts/{attemptId}/resume", c.resume)
		r.Post("/workflows/{workflowId}/attempts/{attemptId}/interventions", c.intervention)
		r.Post("/workflows/{workflowId}/attempts/{attemptId}/resolve", c.resolveAttempt)
		r.Get("/workflows/{workflowId}/messages", c.listMessages)
		r.Post("/workflows/{workflowId}/messages", c.createMessage)
		r.Get("/workflows/{workflowId}/task-requests", c.listTaskRequests)
		r.Post("/workflows/{workflowId}/task-requests", c.createTaskRequest)
		r.Get("/workflows/{workflowId}/task-requests/{requestId}", c.taskRequest)
		r.Post("/workflows/{workflowId}/task-requests/{requestId}/decide", c.decideTaskRequest)
		r.Post("/workflows/{workflowId}/task-requests/{requestId}/materialize", c.materializeTaskRequest)
	})
}

// caller derives the trusted identity for one M2 request.
//
// A principal failure is a 403 and nothing else happens: the check runs before any service call
// so a refused caller costs no work and leaves no row behind.
// Agent 许可还须匹配当前 HTTP daemon 持有的任期，不能借用数据库中新实例的领导权。
func (c *WorkflowsController) caller(
	w http.ResponseWriter,
	r *http.Request,
	facts principal.RequestFacts,
) (principal.Caller, bool) {
	if c.Principals == nil {
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED",
			"This daemon cannot establish caller identity"))
		return principal.Caller{}, false
	}
	facts.Request = r
	facts.AgentSessionID = domain.SessionID(strings.TrimSpace(r.Header.Get(principal.HeaderSessionID)))
	caller, err := c.Principals.Resolve(r.Context(), facts)
	if err != nil {
		envelope.WriteError(w, r, principalError(err))
		return principal.Caller{}, false
	}
	if caller.IsAgent() && facts.RouteAttemptID != "" && facts.RouteAttemptID != caller.AttemptID {
		envelope.WriteError(w, r, apierr.Invalid("COLLAB_ATTEMPT_MISMATCH", "The caller does not own the attempt this route names", nil))
		return principal.Caller{}, false
	}
	if caller.ExecutionPermit != nil {
		if c.ExecutionFence == nil {
			envelope.WriteError(w, r, apierr.NotImplemented("SCHEDULER_FENCE_UNAVAILABLE", "This daemon cannot establish execution ownership"))
			return principal.Caller{}, false
		}
		fence, err := c.ExecutionFence(r.Context())
		if err == nil && fence != caller.ExecutionPermit.Fence {
			err = domain.ErrSchedulerFence
		}
		if err != nil {
			envelope.WriteError(w, r, schedulerError(err))
			return principal.Caller{}, false
		}
		*r = *r.WithContext(ports.WithExecutionPermit(r.Context(), *caller.ExecutionPermit))
	}
	return caller, true
}

// approvalDigestBodyLimit bounds how much of a body the approval proof will buffer.
//
// The approval binds the EXACT bytes of the action, so the handler must digest them before
// decoding them. The ceiling matches the largest operator action body (a bind, which may name a
// prompt); anything larger is refused rather than read.
const approvalDigestBodyLimit = workflowCreateBodyLimit

// operatorApprovalBinding 在所有 M2 handler 解码前绑定原始正文及含 query 的精确 RequestURI。
func operatorApprovalBinding(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bound, ok := withApprovalDigest(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, bound)
	})
}

type approvalBodyDigestKey struct{}

// withApprovalDigest 只为携带审批的请求读取正文，保存同源 SHA 供事务幂等审计使用。
func withApprovalDigest(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	if strings.TrimSpace(r.Header.Get(principal.HeaderOperatorApproval)) == "" {
		return r, true
	}
	if r.Body == nil {
		r.Body = http.NoBody
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, approvalDigestBodyLimit+1))
	_ = r.Body.Close()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON",
			"Could not read the request body", nil)
		return r, false
	}
	if len(raw) > approvalDigestBodyLimit {
		envelope.WriteAPIError(w, r, http.StatusRequestEntityTooLarge, "bad_request", "BODY_TOO_LARGE",
			"The request body is too large to authorise with an approval", nil)
		return r, false
	}
	// The handler must still be able to decode what it was sent.
	r.Body = io.NopCloser(bytes.NewReader(raw))
	// The CONCRETE path is what the human approved; the route pattern would only name the
	// shape. Both ends of the approval therefore travel as the concrete path.
	digest := principal.PayloadDigest(raw)
	r = r.WithContext(context.WithValue(r.Context(), approvalBodyDigestKey{}, strings.TrimPrefix(digest, principal.ApprovalDigestPrefix)))
	return principal.WithApprovalBinding(r, r.Method, r.URL.RequestURI(), digest), true
}

// principalError maps an identity refusal onto the API error envelope.
//
// Every refusal is 403. The underlying sentinel stays in the server log path rather than the
// body: telling a caller which check it failed is a hint it can use to forge the next one.
//
// An approval refusal is the exception, and it is a genuine one: it carries a stable CODE and a
// state REASON ("expired", "consumed", "operation mismatch") that a client must branch on to
// decide whether to request a new approval or to fix its request. None of it is a secret — it
// describes the record the caller is already holding an id for.
func principalError(err error) error {
	if refusal, ok := principal.AsApprovalRefusal(err); ok {
		code := refusal.Code
		if strings.TrimSpace(code) == "" {
			code = principal.CodeApprovalInvalid
		}
		message := refusal.Message
		if strings.TrimSpace(message) == "" {
			message = "The operator approval cannot authorise this request"
		}
		details := map[string]any{}
		if refusal.Reason != "" {
			details["reason"] = refusal.Reason
		}
		return apierr.New(apierr.KindForbidden, code, message, details)
	}
	switch {
	case errors.Is(err, principal.ErrSecurityUpgradeRequired):
		return apierr.Forbidden(principal.CodeSecurityUpgradeRequired, err.Error())
	case errors.Is(err, principal.ErrNotAuthorized),
		errors.Is(err, principal.ErrCapabilityInvalid),
		errors.Is(err, principal.ErrLeaseUnproven):
		return apierr.Forbidden("COLLAB_NOT_AUTHORIZED",
			"The caller is not authorized to act on this resource")
	}
	return err
}

// capabilities reports the graded capability evidence AO holds for each launch harness.
func (c *WorkflowsController) capabilities(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.caller(w, r, principal.RequestFacts{}); !ok {
		return
	}
	if c.Capabilities == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/workflows/capabilities")
		return
	}
	harnesses := []domain.AgentHarness{
		domain.HarnessCodex, domain.HarnessClaudeCode, domain.HarnessOMP, domain.HarnessDeepSeek,
	}
	out := make([]WorkflowCapabilityResponse, 0, len(harnesses))
	now := c.now()
	for _, harness := range harnesses {
		snapshot, err := c.Capabilities.Resolve(r.Context(), ports.CapabilityProbeRequest{Harness: harness})
		if err != nil {
			// A probe failure is reported as unknown evidence for that harness rather than
			// failing the whole matrix: one unreachable provider must not hide the others.
			snapshot = domain.NewHarnessCapabilitySnapshot(
				harness, domain.CapabilitySourceStatic, now, time.Minute,
			)
			snapshot.ReasonCode = ports.CapabilityReasonProbeFailed
		}
		out = append(out, workflowCapabilityResponse(snapshot, now))
	}
	envelope.WriteJSON(w, http.StatusOK, GetWorkflowCapabilitiesResponse{Capabilities: out})
}

// binding reads the binding projection for one attempt.
//
// The URL's workflow id is checked against the binding before anything is returned. Without that
// check an attempt id from one workflow would let a caller read another workflow's binding and
// repository facts; a mismatch answers 404 and not 409, so the response never confirms that the
// attempt exists somewhere else.
func (c *WorkflowsController) binding(w http.ResponseWriter, r *http.Request) {
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	attemptID := domain.WorkflowAttemptID(chi.URLParam(r, "attemptId"))
	caller, ok := c.caller(w, r, principal.RequestFacts{WorkflowID: workflowID, RouteAttemptID: attemptID})
	if !ok {
		return
	}
	if caller.IsAgent() && caller.AttemptID != attemptID {
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED",
			"An agent may only read its own attempt's binding"))
		return
	}
	if c.Bindings == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/binding")
		return
	}
	binding, found, err := c.Bindings.GetWorkflowAttemptBinding(r.Context(), attemptID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !found || binding.WorkflowID != workflowID {
		envelope.WriteError(w, r, apierr.NotFound("WORKFLOW_BINDING_NOT_FOUND",
			"No binding exists for this attempt in this workflow"))
		return
	}
	repos, err := c.Bindings.ListWorkflowAttemptRepoBindings(r.Context(), attemptID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowBindingEnvelope{
		Binding: workflowBindingResponse(binding, repos),
	})
}

// bind creates or resumes the attempt's binding intent.
//
// Every fact the bind sends comes from durable state:
//
//   - the task, project, harness and repositories are resolved from the URL's workflow and
//     attempt (OwnershipResolver), never from the body, so a caller cannot borrow another
//     task's project or widen a repository scope;
//   - the body may only NARROW the repository set, via repoKeys, and a key the task does not
//     own is refused before the binder is called;
//   - a binding that already exists must agree with the resolved ownership, otherwise the bind
//     is refused rather than silently re-pointed.
//
// The previous version sent ProjectID:"" and, on a first bind, an empty TaskID, which
// ports.SpawnRequest.Validate() rejects — so the route could not work at all. That is the
// B-03 defect this replaces.
func (c *WorkflowsController) bind(w http.ResponseWriter, r *http.Request) {
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	attemptID := domain.WorkflowAttemptID(chi.URLParam(r, "attemptId"))
	caller, ok := c.caller(w, r, principal.RequestFacts{
		WorkflowID: workflowID, RouteAttemptID: attemptID,
	})
	if !ok {
		return
	}
	if !caller.IsOperator() {
		// Binding provisions a session for a task. An agent is the work inside that session, not
		// the party that creates it, so it may never bind or re-bind its own attempt.
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED",
			"Only an operator may bind an attempt"))
		return
	}
	if c.Binder == nil || c.Ownership == nil || c.Bindings == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/bind")
		return
	}
	var req BindWorkflowAttemptRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_IDEMPOTENCY_KEY",
			"idempotencyKey is required", nil)
		return
	}

	ownership, err := c.Ownership.ResolveAttemptOwnership(r.Context(), workflowID, attemptID)
	if err != nil {
		envelope.WriteError(w, r, bindError(err))
		return
	}
	// The resolver is asked about the URL's workflow, but the answer is checked again: an
	// adapter that looked the attempt up by id alone would otherwise let a cross-workflow bind
	// through, and this is the place that catches it.
	if ownership.Attempt.WorkflowID != workflowID || ownership.Task.WorkflowID != workflowID {
		envelope.WriteError(w, r, apierr.Conflict("WORKFLOW_BIND_CONFLICT",
			"That attempt does not belong to this workflow", nil))
		return
	}
	if strings.TrimSpace(string(ownership.Task.ID)) == "" || strings.TrimSpace(string(ownership.ProjectID)) == "" {
		// No project can be derived: binding without one would produce a spawn request that
		// Validate() refuses, so refuse here with a code that says why.
		envelope.WriteError(w, r, apierr.Conflict("WORKFLOW_PROJECT_UNRESOLVED",
			"No project can be derived for that attempt's task", nil))
		return
	}

	harness := req.Harness
	if harness == "" {
		harness = ownership.Harness
	}
	if harness == "" {
		harness = ownership.Task.AgentHarness
	}

	repositories, ok := selectBoundRepositories(w, r, ownership.Repos, req.RepoKeys)
	if !ok {
		return
	}

	// An existing binding must already agree with the resolved ownership. A binding that names
	// another task or workflow is evidence that the durable facts and the binding disagree, and
	// binding over it would launder the difference.
	existing, found, err := c.Bindings.GetWorkflowAttemptBinding(r.Context(), attemptID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if found {
		if existing.WorkflowID != workflowID || existing.TaskID != ownership.Task.ID {
			envelope.WriteError(w, r, apierr.Conflict("WORKFLOW_BIND_CONFLICT",
				"The existing binding does not match this attempt's task", nil))
			return
		}
	}

	outcome, err := c.Binder.BindAttempt(r.Context(), ports.SpawnRequest{
		WorkflowID: workflowID,
		TaskID:     ownership.Task.ID,
		AttemptID:  attemptID,
		SpawnKey:   req.IdempotencyKey,
		ProjectID:  ownership.ProjectID,
		Harness:    harness,
		Model:      ownership.Task.Model, Effort: ownership.Task.Effort, Profile: ownership.Task.Profile,
		Mode:   req.Mode,
		Prompt: req.Prompt,
		Repos:  repositoryRefs(repositories),
	}, repositories)
	if err != nil {
		envelope.WriteError(w, r, bindError(err))
		return
	}

	repos, listErr := c.Bindings.ListWorkflowAttemptRepoBindings(r.Context(), attemptID)
	if listErr != nil {
		envelope.WriteError(w, r, listErr)
		return
	}
	// A frozen outcome is not success. It answers 409 with a stable code, so a client cannot
	// read a stopped-at-unknown bind as "ready".
	if outcome.Frozen {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", bindReasonCode(outcome.ReasonCode),
			"The bind did not complete; no further action may be taken until it is reconciled",
			map[string]any{
				"binding":    workflowBindingResponse(outcome.Binding, repos),
				"reasonCode": outcome.ReasonCode,
				"frozen":     true,
			})
		return
	}
	envelope.WriteJSON(w, http.StatusOK, BindWorkflowAttemptResponse{
		Binding:    workflowBindingResponse(outcome.Binding, repos),
		Spawned:    outcome.Spawned,
		Frozen:     outcome.Frozen,
		ReasonCode: outcome.ReasonCode,
	})
}

// selectBoundRepositories narrows the resolved repository set to the keys the caller asked for.
//
// The task's owned repositories are the authority: this function can only shrink the set. A
// requested key outside that scope is a 409 for the same reason the whole bind is 409-typed —
// the caller asked for something the durable facts do not permit, and no work should happen.
func selectBoundRepositories(
	w http.ResponseWriter,
	r *http.Request,
	owned []domain.WorkflowAttemptRepoBinding,
	requested []string,
) ([]domain.WorkflowAttemptRepoBinding, bool) {
	if len(requested) == 0 {
		return owned, true
	}
	byKey := make(map[domain.WorkflowRepoKey]domain.WorkflowAttemptRepoBinding, len(owned))
	for _, repo := range owned {
		byKey[repo.RepoKey] = repo
	}
	selected := make([]domain.WorkflowAttemptRepoBinding, 0, len(requested))
	seen := make(map[domain.WorkflowRepoKey]struct{}, len(requested))
	for _, raw := range requested {
		key := domain.WorkflowRepoKey(strings.TrimSpace(raw))
		if key == "" {
			envelope.WriteError(w, r, apierr.Invalid("WORKFLOW_REPO_SCOPE_INVALID",
				"A repository key cannot be empty", nil))
			return nil, false
		}
		repo, exists := byKey[key]
		if !exists {
			// Named without its value: the refusal says the scope does not include it without
			// listing what it does include.
			envelope.WriteError(w, r, apierr.Conflict("WORKFLOW_BIND_CONFLICT",
				"That repository is not in this task's scope", nil))
			return nil, false
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		selected = append(selected, repo)
	}
	return selected, true
}

// repositoryRefs projects bound repositories for the spawn request.
func repositoryRefs(repos []domain.WorkflowAttemptRepoBinding) []ports.WorkflowRepoKeyRef {
	if len(repos) == 0 {
		return nil
	}
	refs := make([]ports.WorkflowRepoKeyRef, 0, len(repos))
	for _, repo := range repos {
		refs = append(refs, ports.WorkflowRepoKeyRef{
			RepoKey: repo.RepoKey, ProjectID: repo.ProjectID, BaseSHA: repo.ExpectedBaseSHA,
		})
	}
	return refs
}

// requireAttemptInWorkflow refuses when the attempt's persisted binding does not belong to the
// URL's workflow.
//
// It answers 404, not 409: a refusal that distinguished "belongs elsewhere" from "does not exist"
// would confirm the existence of an attempt in another workflow, and confirming that is the whole
// disclosure a cross-workflow probe is after. Every attempt-scoped M2 route that reads or writes
// through a binding calls this before it touches the service, so the check cannot be forgotten on
// a new route without also losing the binding read it needs.
func (c *WorkflowsController) requireAttemptInWorkflow(
	w http.ResponseWriter,
	r *http.Request,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
) (domain.WorkflowAttemptBinding, bool) {
	if c.Bindings == nil {
		envelope.WriteError(w, r, apierr.NotFound("WORKFLOW_BINDING_NOT_FOUND",
			"No binding exists for this attempt in this workflow"))
		return domain.WorkflowAttemptBinding{}, false
	}
	binding, found, err := c.Bindings.GetWorkflowAttemptBinding(r.Context(), attemptID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return domain.WorkflowAttemptBinding{}, false
	}
	if !found || binding.WorkflowID != workflowID {
		envelope.WriteError(w, r, apierr.NotFound("WORKFLOW_BINDING_NOT_FOUND",
			"No binding exists for this attempt in this workflow"))
		return domain.WorkflowAttemptBinding{}, false
	}
	return binding, true
}

// interventionHarness resolves the harness the capability gate must be asked about.
//
// # Why the body may not supply it
//
// The harness chooses WHICH capability evidence the gate consults. A caller that could name it
// could pick the harness whose description happens to allow the steer it wants, which makes the
// gate advisory rather than a check. So the trusted answer comes from durable state:
//
//  1. the attempt's ownership (OwnershipResolver), which is the task's own harness — the same
//     source the bind route uses; and
//  2. the live session observation when ownership cannot name one, because a bound session is
//     what the intervention actually addresses.
//
// A harness in the request body is never a source. It is only a CHECK: when it disagrees with the
// resolved harness the request answers 409 WORKFLOW_HARNESS_MISMATCH, because silently preferring
// either one would let a caller believe it steered a harness it did not.
//
// No harness at all is a 409 for the same reason: without one there is no evidence to consult,
// and guessing is the fabrication the capability boundary exists to prevent.
func (c *WorkflowsController) interventionHarness(
	w http.ResponseWriter,
	r *http.Request,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
	requested domain.AgentHarness,
) (domain.AgentHarness, bool) {
	resolved := domain.AgentHarness("")
	if c.Ownership != nil {
		if ownership, err := c.Ownership.ResolveAttemptOwnership(r.Context(), workflowID, attemptID); err == nil {
			// The ownership answer is checked against the URL again: an adapter that looked the
			// attempt up by id alone must not let another workflow's task supply the harness.
			if ownership.Attempt.WorkflowID == workflowID || ownership.Attempt.WorkflowID == "" {
				resolved = ownership.Harness
				if resolved == "" {
					resolved = ownership.Task.AgentHarness
				}
			}
		}
		// An ownership error is not fatal here: the live observation below is the second source,
		// and a request that neither source can answer is refused below rather than guessed at.
	}
	if resolved == "" && c.Binder != nil {
		if observation, _, err := c.Binder.ObserveSession(r.Context(), attemptID); err == nil {
			resolved = observation.Harness
		}
	}
	if requested != "" {
		if resolved == "" {
			envelope.WriteError(w, r, apierr.Conflict("WORKFLOW_HARNESS_MISMATCH",
				"The request names a harness that this attempt's durable state does not confirm", nil))
			return "", false
		}
		if requested != resolved {
			envelope.WriteError(w, r, apierr.Conflict("WORKFLOW_HARNESS_MISMATCH",
				"The request names a harness that does not match this attempt", nil))
			return "", false
		}
	}
	if resolved == "" {
		envelope.WriteError(w, r, apierr.Conflict("WORKFLOW_HARNESS_MISMATCH",
			"No harness can be derived for this attempt", nil))
		return "", false
	}
	return resolved, true
}

// observe reports the attempt's session facts and binding state.
//
// The workflow id is verified against the persisted binding first, so an attempt id from another
// workflow answers 404 rather than disclosing its session. Live and reasonCode are added only
// when the wired binder can run the coordinator's own liveness check: absent means "this
// deployment cannot answer", which is different from live=false.
func (c *WorkflowsController) observe(w http.ResponseWriter, r *http.Request) {
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	attemptID := domain.WorkflowAttemptID(chi.URLParam(r, "attemptId"))
	caller, ok := c.caller(w, r, principal.RequestFacts{
		WorkflowID: workflowID, RouteAttemptID: attemptID,
	})
	if !ok {
		return
	}
	if caller.IsAgent() && caller.AttemptID != attemptID {
		// An agent may read facts about its own attempt — that is how it checks its session — but
		// not about a sibling's.
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED",
			"An agent may only observe its own attempt"))
		return
	}
	if c.Binder == nil || c.Bindings == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/observe")
		return
	}
	if _, ok := c.requireAttemptInWorkflow(w, r, workflowID, attemptID); !ok {
		return
	}

	observation, state, err := c.Binder.ObserveSession(r.Context(), attemptID)
	if err != nil {
		envelope.WriteError(w, r, bindError(err))
		return
	}
	response := ObserveWorkflowAttemptResponse{
		Observation:  workflowSessionObservationResponse(observation),
		BindingState: string(state),
	}
	if live, ok := c.Binder.(AttemptLiveTarget); ok {
		// The coordinator's own check, not a second opinion: it reads the current gateway
		// observation and applies the same rule its send and interrupt paths apply.
		_, liveObservation, isLive, reason, liveErr := live.LiveTarget(r.Context(), attemptID)
		if liveErr != nil {
			envelope.WriteError(w, r, bindError(liveErr))
			return
		}
		if response.Observation == nil && liveObservation.SessionID != "" {
			response.Observation = workflowSessionObservationResponse(liveObservation)
		}
		value := isLive
		response.Live = &value
		response.ReasonCode = reason
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

// reconcile gathers evidence for an unknown spawn and never spawns again.
//
// It is idempotent in the only sense that matters here: calling it twice on a settled binding
// answers "not_unknown" with the current row and changes nothing.
func (c *WorkflowsController) reconcile(w http.ResponseWriter, r *http.Request) {
	if c.Binder == nil || c.Bindings == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/reconcile")
		return
	}
	c.recoveryPost(w, r,
		func(ctx context.Context, attemptID domain.WorkflowAttemptID) (executionsvc.BindOutcome, error) {
			return c.Binder.Reconcile(ctx, attemptID)
		})
}

// resume re-attaches a binding to the session that already exists after a restart.
//
// Resume is OPTIONAL: *execution.Coordinator answers 501 here until execution.Coordinator.Resume
// lands, and the route becomes live with no change to this package. A 501 is the honest answer
// for "this daemon cannot recover a binding", and it is never a 200 with an unchanged binding.
func (c *WorkflowsController) resume(w http.ResponseWriter, r *http.Request) {
	if c.Binder == nil || c.Bindings == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/resume")
		return
	}
	resumer, ok := c.Binder.(AttemptResumer)
	if !ok {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/resume")
		return
	}
	c.recoveryPost(w, r, resumer.Resume)
}

// recoveryPost is the shared body of reconcile and resume.
//
// Both answer the same question — what does this binding look like now, and did this call change
// it — so they share the ownership check, the frozen→409 rule, and the 200 shape the bind route
// uses. Neither can report Spawned=true: recovery adopts or re-attaches, it never creates.
func (c *WorkflowsController) recoveryPost(
	w http.ResponseWriter,
	r *http.Request,
	act func(ctx context.Context, attemptID domain.WorkflowAttemptID) (executionsvc.BindOutcome, error),
) {
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	attemptID := domain.WorkflowAttemptID(chi.URLParam(r, "attemptId"))
	caller, ok := c.caller(w, r, principal.RequestFacts{
		WorkflowID: workflowID, RouteAttemptID: attemptID,
	})
	if !ok {
		return
	}
	if !caller.IsOperator() {
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED",
			"Only an operator may reconcile or resume an attempt"))
		return
	}
	if _, ok := c.requireAttemptInWorkflow(w, r, workflowID, attemptID); !ok {
		return
	}

	outcome, err := act(r.Context(), attemptID)
	if err != nil {
		envelope.WriteError(w, r, bindError(err))
		return
	}
	repos, listErr := c.Bindings.ListWorkflowAttemptRepoBindings(r.Context(), attemptID)
	if listErr != nil {
		envelope.WriteError(w, r, listErr)
		return
	}
	if outcome.Frozen {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", bindReasonCode(outcome.ReasonCode),
			"The binding is still uncertain; nothing further may be attempted until it is resolved",
			map[string]any{
				"binding":    workflowBindingResponse(outcome.Binding, repos),
				"reasonCode": outcome.ReasonCode,
				"frozen":     true,
			})
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReconcileWorkflowAttemptResponse{
		Binding: workflowBindingResponse(outcome.Binding, repos),
		// Recovery never spawns, and the field is stated rather than omitted so a client that
		// pipes this through the bind handler cannot mistake adoption for creation.
		Spawned:    false,
		Frozen:     outcome.Frozen,
		ReasonCode: outcome.ReasonCode,
	})
}

// workflowSessionObservationResponse projects one observation, or nil when there is none.
//
// An empty observation means the binding has no session yet, which is not the same as a session
// with no facts: the first is absent, the second is a row with an empty id.
func workflowSessionObservationResponse(
	observation ports.SessionObservation,
) *WorkflowSessionObservationResponse {
	if observation.SessionID == "" {
		return nil
	}
	return &WorkflowSessionObservationResponse{
		SessionID:              string(observation.SessionID),
		ProjectID:              string(observation.ProjectID),
		Kind:                   string(observation.Kind),
		Harness:                string(observation.Harness),
		Mode:                   string(observation.Mode),
		Readiness:              string(observation.Readiness),
		ControllerGeneration:   observation.ControllerGeneration,
		ProviderConversationID: observation.ProviderConversationID,
		ClientRequestID:        observation.ClientRequestID,
		ClientRequestCommitted: observation.ClientRequestCommitted,
		Terminated:             observation.Terminated,
		ObservedAt:             observation.ObservedAt,
	}
}

// intervention dispatches Steer, Queue, or Stop.
func (c *WorkflowsController) intervention(w http.ResponseWriter, r *http.Request) {
	if c.Interventions == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/interventions")
		return
	}
	var req WorkflowInterventionRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	attemptID := domain.WorkflowAttemptID(chi.URLParam(r, "attemptId"))
	caller, ok := c.caller(w, r, principal.RequestFacts{
		WorkflowID: workflowID, RouteAttemptID: attemptID,
	})
	if !ok {
		return
	}
	if strings.TrimSpace(req.ClientMessageID) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "WORKFLOW_MESSAGE_INVALID",
			"clientMessageId is required", nil)
		return
	}

	// Cross-workflow isolation, then the task id: both come from the persisted binding, never
	// from the body, and from the SAME read so the guard and the id cannot disagree. Without this
	// an attempt bound in one workflow could be steered, queued, or stopped through another
	// workflow's URL, which is exactly the isolation every other attempt-scoped route enforces.
	binding, ok := c.requireAttemptInWorkflow(w, r, workflowID, attemptID)
	if !ok {
		return
	}
	taskID := binding.TaskID

	// The harness is what the capability gate is asked about, so it may not come from the body:
	// a caller that could name the harness could name the one whose evidence happens to allow the
	// steer it wants. It comes from the attempt's durable ownership, or from the live session when
	// ownership cannot name one. A body harness is only ever a CHECK against that answer.
	harness, ok := c.interventionHarness(w, r, workflowID, attemptID, req.Harness)
	if !ok {
		return
	}

	var (
		outcome interventionsvc.Outcome
		replan  *domain.ReplanRequest
		err     error
	)
	// The caller's observed generation is forwarded on every mode. Omitting it would make the
	// fence unreachable: the coordinator would always fall back to the binding's generation and
	// could never tell a caller looking at an older controller that its request is stale.
	switch domain.WorkflowInterventionMode(req.Mode) {
	case domain.WorkflowInterventionModeSteer:
		outcome, err = c.Interventions.Steer(r.Context(), interventionsvc.SteerRequest{
			WorkflowID: workflowID, TaskID: taskID, AttemptID: attemptID,
			ClientMessageID: req.ClientMessageID, Text: req.Text,
			TargetProviderTurnID: req.TargetProviderTurnID, Harness: harness,
			ControllerGeneration: req.ControllerGeneration,
		})
	case domain.WorkflowInterventionModeQueue:
		outcome, err = c.Interventions.Queue(r.Context(), interventionsvc.QueueRequest{
			WorkflowID: workflowID, TaskID: taskID, AttemptID: attemptID,
			ClientMessageID: req.ClientMessageID, Text: req.Text,
			ControllerGeneration: req.ControllerGeneration,
			AuthoredByOperator:   caller.IsOperator(),
		})
	case domain.WorkflowInterventionModeStop:
		outcome, replan, err = c.Interventions.Stop(r.Context(), interventionsvc.StopRequest{
			WorkflowID: workflowID, TaskID: taskID, AttemptID: attemptID,
			ClientMessageID:      req.ClientMessageID,
			ControllerGeneration: req.ControllerGeneration,
		})
	default:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "WORKFLOW_MESSAGE_INVALID",
			"mode must be steer, queue, or stop", nil)
		return
	}
	if err != nil {
		envelope.WriteError(w, r, interventionError(err))
		return
	}

	response := WorkflowInterventionEnvelope{
		Intervention:   workflowInterventionResponse(outcome.Intervention),
		Performed:      outcome.Performed,
		Frozen:         outcome.Frozen,
		QueueAvailable: outcome.QueueAvailable,
		ReasonCode:     outcome.ReasonCode,
	}
	if replan != nil {
		response.Replan = &WorkflowReplanResponse{
			InterventionID: replan.InterventionID, WorkflowID: string(replan.WorkflowID),
			TaskID: string(replan.TaskID), ReasonCode: replan.ReasonCode,
			PreservedWorktrees: replan.PreservedWorktrees, RequestedAt: replan.RequestedAt,
		}
	}

	// A refusal that produced no receipt is not a 200: it answers 409 with the reason and
	// queueAvailable, so a client cannot read a refused steer as "steered".
	if outcome.Intervention.ID == "" && outcome.ReasonCode != "" {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", interventionReasonCode(outcome.ReasonCode),
			"The intervention was refused and nothing was sent",
			map[string]any{"queueAvailable": outcome.QueueAvailable, "reasonCode": outcome.ReasonCode})
		return
	}
	status := http.StatusOK
	if outcome.Frozen {
		// Uncertain is not success, and it is not a plain 200 either.
		status = http.StatusAccepted
	}
	envelope.WriteJSON(w, status, response)
}

// resolveAttempt records an operator's decided outcome for an expired attempt.
//
// This is the only exit from UNKNOWN and it changes an attempt's final state, so it is an
// operator decision and nothing else:
//
//   - the caller must be the operator (a worker may not decide its own attempt's fate);
//   - a lease-proven caller is an agent acting as an attempt, and is refused for the same reason;
//   - a caller whose identity is absent is refused before the service is called, so a refused
//     request leaves the attempt untouched;
//   - an attempt that belongs to another workflow answers 404 rather than being resolved through
//     this URL, and the store's own workflow-scoped CAS is the second line of that check.
//
// The decision is appended to the audit trail. A duplicate request with the same idempotency
// material replays the stored attempt and appends nothing: the CAS already refused to change
// anything, so a second row would claim a decision that was never taken.
func (c *WorkflowsController) resolveAttempt(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/attempts/{attemptId}/resolve")
		return
	}
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	attemptID := domain.WorkflowAttemptID(chi.URLParam(r, "attemptId"))
	caller, ok := c.caller(w, r, principal.RequestFacts{WorkflowID: workflowID, RouteAttemptID: attemptID})
	if !ok {
		return
	}
	if !caller.IsOperator() || caller.LeaseProven {
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED",
			"Only an operator may resolve an attempt"))
		return
	}
	var req ResolveWorkflowAttemptRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if !c.requireAttemptOwnershipInWorkflow(w, r, workflowID, attemptID) {
		return
	}
	attempt, applied, err := c.Svc.Resolve(r.Context(), workflowsvc.ResolveInput{
		WorkflowID:           workflowID,
		AttemptID:            attemptID,
		Outcome:              domain.WorkflowAttemptState(req.Outcome),
		ErrorCode:            req.ErrorCode,
		ExpectedTaskRevision: req.ExpectedTaskRevision,
		IdempotencyKey:       req.IdempotencyKey,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if applied && !c.recordDecision(r, principal.AuditRecord{
		ActorKind: caller.Kind, ActorRef: caller.Ref(),
		WorkflowID: workflowID, AttemptID: attemptID,
		Decision: "resolve_attempt", Outcome: string(attempt.State),
		TokenFingerprint: caller.OperatorTokenFingerprint,
	}) {
		envelope.WriteError(w, r, apierr.Internal("WORKFLOW_AUDIT_UNAVAILABLE",
			"The decision was applied but could not be recorded in the audit trail"))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowAttemptEnvelope{Attempt: workflowAttemptResponse(attempt)})
}

// requireAttemptOwnershipInWorkflow refuses when the attempt does not belong to the URL's
// workflow, without changing anything.
//
// It answers 404 for the same reason requireAttemptInWorkflow does: distinguishing "belongs
// elsewhere" from "does not exist" would confirm the existence of another workflow's attempt,
// which is the disclosure a cross-workflow probe is after. The ownership resolver is the source
// when one is wired; without it the attempt cannot be located at all, so nothing may be resolved
// rather than everything being allowed.
func (c *WorkflowsController) requireAttemptOwnershipInWorkflow(
	w http.ResponseWriter,
	r *http.Request,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
) bool {
	if c.Ownership == nil {
		envelope.WriteError(w, r, apierr.NotFound("WORKFLOW_ATTEMPT_NOT_FOUND",
			"No such attempt in this workflow"))
		return false
	}
	ownership, err := c.Ownership.ResolveAttemptOwnership(r.Context(), workflowID, attemptID)
	if err != nil {
		// A cross-workflow answer is a 404 here rather than the bind route's 409: this route
		// discloses nothing about the attempt, and no state may change.
		if errors.Is(err, executionsvc.ErrWorkflowMismatch) ||
			errors.Is(err, executionsvc.ErrAttemptNotFound) ||
			errors.Is(err, executionsvc.ErrTaskNotFound) ||
			errors.Is(err, domain.ErrCollaborationCrossWorkflow) {
			envelope.WriteError(w, r, apierr.NotFound("WORKFLOW_ATTEMPT_NOT_FOUND",
				"No such attempt in this workflow"))
			return false
		}
		envelope.WriteError(w, r, err)
		return false
	}
	if ownership.Attempt.WorkflowID != workflowID || ownership.Task.WorkflowID != workflowID {
		envelope.WriteError(w, r, apierr.NotFound("WORKFLOW_ATTEMPT_NOT_FOUND",
			"No such attempt in this workflow"))
		return false
	}
	return true
}

// listMessages returns one page of a workflow's collaboration messages.
func (c *WorkflowsController) listMessages(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.caller(w, r, principal.RequestFacts{
		WorkflowID: domain.WorkflowID(chi.URLParam(r, "workflowId")),
	}); !ok {
		return
	}
	if c.Collaboration == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/workflows/{workflowId}/messages")
		return
	}
	limit, offset, code, message := workflowPage(r)
	if code != "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", code, message, nil)
		return
	}
	items, err := c.Collaboration.ListMessages(
		r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")), limit, offset,
	)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	messages := make([]WorkflowMessageResponse, 0, len(items))
	for _, item := range items {
		messages = append(messages, workflowMessageResponse(item))
	}
	envelope.WriteJSON(w, http.StatusOK, ListWorkflowMessagesResponse{
		Messages:   messages,
		NextCursor: workflowNextCursor(offset, len(items), limit),
	})
}

// createMessage stores one collaboration message.
//
// The sender's attempt comes from the proven caller, never from the body. A caller with no proven
// attempt - the operator - cannot post AS a task, because there is no task whose identity it
// holds; that is a 403 rather than a message attributed to nobody.
func (c *WorkflowsController) createMessage(w http.ResponseWriter, r *http.Request) {
	caller, ok := c.caller(w, r, principal.RequestFacts{
		WorkflowID: domain.WorkflowID(chi.URLParam(r, "workflowId")),
	})
	if !ok {
		return
	}
	if c.Collaboration == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/messages")
		return
	}
	if !caller.LeaseProven || caller.AttemptID == "" {
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED",
			"A message must be sent from a proven workflow attempt"))
		return
	}
	var req CreateWorkflowMessageRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	var toTaskID *domain.WorkflowTaskID
	if strings.TrimSpace(req.ToTaskID) != "" {
		value := domain.WorkflowTaskID(req.ToTaskID)
		toTaskID = &value
	}
	outcome, err := c.Collaboration.CreateMessage(r.Context(), domain.CreateWorkflowMessageInput{
		WorkflowID:      domain.WorkflowID(chi.URLParam(r, "workflowId")),
		AttemptID:       caller.AttemptID,
		ToTaskID:        toTaskID,
		Kind:            domain.WorkflowMessageKind(req.Kind),
		Body:            req.Body,
		PayloadRef:      req.PayloadRef,
		ClientMessageID: req.ClientMessageID,
	})
	if err != nil {
		envelope.WriteError(w, r, collaborationError(err))
		return
	}
	status := http.StatusOK
	if outcome.Created {
		status = http.StatusCreated
	}
	envelope.WriteJSON(w, status, CreateWorkflowMessageResponse{
		Message: workflowMessageResponse(outcome.Message),
		Created: outcome.Created,
	})
}

// createTaskRequest stores a request awaiting an owner decision.
func (c *WorkflowsController) createTaskRequest(w http.ResponseWriter, r *http.Request) {
	caller, ok := c.caller(w, r, principal.RequestFacts{
		WorkflowID: domain.WorkflowID(chi.URLParam(r, "workflowId")),
	})
	if !ok {
		return
	}
	if c.Collaboration == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/task-requests")
		return
	}
	if !caller.LeaseProven || caller.AttemptID == "" {
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED",
			"A task request must be raised from a proven workflow attempt"))
		return
	}
	var req CreateWorkflowTaskRequestRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	outcome, err := c.Collaboration.CreateTaskRequest(r.Context(), collaborationsvc.CreateTaskRequestInput{
		WorkflowID:  domain.WorkflowID(chi.URLParam(r, "workflowId")),
		AttemptID:   caller.AttemptID,
		RequestID:   req.RequestID,
		RequestKind: domain.WorkflowTaskRequestKind(req.RequestKind),
		Payload:     req.Payload,
	})
	if err != nil {
		envelope.WriteError(w, r, collaborationError(err))
		return
	}
	status := http.StatusOK
	if outcome.Created {
		status = http.StatusCreated
	}
	envelope.WriteJSON(w, status, CreateWorkflowTaskRequestResponse{
		Request: workflowTaskRequestResponse(outcome.Request),
		Created: outcome.Created,
	})
}

// decideTaskRequest records an owner decision.
func (c *WorkflowsController) decideTaskRequest(w http.ResponseWriter, r *http.Request) {
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	caller, ok := c.caller(w, r, principal.RequestFacts{WorkflowID: workflowID})
	if !ok {
		return
	}
	if c.Collaboration == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/workflows/{workflowId}/task-requests/{requestId}/decide")
		return
	}
	var req DecideWorkflowTaskRequestRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	// Who decided is derived, not supplied. An operator is the human decider; an agent is
	// refused outright, and its attempt id is passed through so collaboration.Decide's own
	// refusal fires as the second line rather than being the only one.
	if caller.IsAgent() || caller.LeaseProven {
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED",
			"Only a human owner may decide a task request"))
		return
	}
	// Cross-workflow isolation, checked before the decision so a request id from another
	// workflow can never be decided through this URL. The lookup is scoped to the URL's workflow,
	// so a request that lives elsewhere is simply absent — the caller learns nothing about
	// whether it exists.
	prior, ok := c.requireTaskRequestInWorkflow(w, r, workflowID, chi.URLParam(r, "requestId"))
	if !ok {
		return
	}
	decision, err := c.Collaboration.Decide(r.Context(), collaborationsvc.DecideInput{
		WorkflowID:       workflowID,
		RequestID:        chi.URLParam(r, "requestId"),
		Approve:          req.Approve,
		DecidedBy:        domain.WorkflowTaskRequestDeciderHuman,
		DecidedByRef:     operatorDecidedByRef(caller),
		Reason:           req.Reason,
		ExpectedRevision: req.ExpectedRevision,
		DeciderAttemptID: caller.AttemptID,
	})
	if err != nil {
		// A replay of the SAME decision is not a conflict, it is the answer to a retry: the store's
		// revision+state CAS already refused to change anything, so the stored decision is returned
		// and NOTHING is appended to the audit trail. A second audit row here would claim a
		// decision that was never taken.
		if errors.Is(err, domain.ErrWorkflowTaskRequestConflict) {
			if replay, ok := replayedDecision(prior, req); ok {
				// A replay reports the state the FIRST call produced. Materialization is derived
				// from that stored state, never from the retried request, so a retry cannot make a
				// rejection look materialized or invent a task.
				materialization, ok := c.persistedMaterialization(w, r, replay, materializationFor(replay.State))
				if !ok {
					return
				}
				envelope.WriteJSON(w, decideStatus(materialization), DecideWorkflowTaskRequestResponse{
					Request:         workflowTaskRequestResponse(replay),
					Materialization: string(materialization),
					Materialized:    materialization.Materialized(),
					ReasonCode:      domain.TaskRequestReasonAlreadyDecided,
					Message:         decideMessage(materialization),
				})
				return
			}
		}
		envelope.WriteError(w, r, collaborationError(err))
		return
	}
	if !c.recordDecision(r, principal.AuditRecord{
		ActorKind: caller.Kind, ActorRef: caller.Ref(),
		WorkflowID: workflowID, RequestID: chi.URLParam(r, "requestId"),
		Decision: "decide_task_request", Outcome: string(decision.Request.State),
		TokenFingerprint: caller.OperatorTokenFingerprint,
	}) {
		envelope.WriteError(w, r, apierr.Internal("WORKFLOW_AUDIT_UNAVAILABLE",
			"The decision was applied but could not be recorded in the audit trail"))
		return
	}
	// Materialization is resolved against the STORED request state rather than trusted blindly
	// from the coordinator's field, so the response's state and its materialization can never
	// contradict each other — and a future coordinator cannot report "materialized" for an
	// approval that left no task row.
	materialization, ok := c.persistedMaterialization(w, r, decision.Request, decisionMaterialization(decision))
	if !ok {
		return
	}
	envelope.WriteJSON(w, decideStatus(materialization), DecideWorkflowTaskRequestResponse{
		Request:         workflowTaskRequestResponse(decision.Request),
		Materialization: string(materialization),
		Materialized:    materialization.Materialized(),
		ReasonCode:      decision.ReasonCode,
		Message:         decideMessage(materialization),
	})
}

// materializeTaskRequest 独立审批本次图 revision 与原申请 SHA，不复用 Decide 已消费的批准。
func (c *WorkflowsController) materializeTaskRequest(w http.ResponseWriter, r *http.Request) {
	if c.Materializer == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	var req MaterializeTaskRequestRequest
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	change := policyChange(r, req.IdempotencyKey, req.Reason, caller)
	result, created, err := c.Materializer.MaterializeTaskRequest(r.Context(), domain.TaskRequestMaterializationInput{
		WorkflowID: domain.WorkflowID(chi.URLParam(r, "workflowId")), RequestID: chi.URLParam(r, "requestId"), RequestSHA: req.RequestSHA,
		ExpectedRequestRevision: req.ExpectedRequestRevision, ExpectedWorkflowRevision: req.ExpectedWorkflowRevision,
		IdempotencyKey: req.IdempotencyKey, RequestFingerprint: change.RequestFingerprint, ActorRef: change.ActorRef, Reason: req.Reason, Now: change.Now,
	}, caller)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	envelope.WriteJSON(w, status, MaterializeTaskRequestResponse{Materialization: TaskRequestMaterializationResponse{RequestID: result.RequestID, WorkflowID: string(result.WorkflowID), TaskID: string(result.TaskID), WorkflowRevision: result.WorkflowRevision, CreatedAt: result.CreatedAt}, Created: created})
}

// persistedMaterialization 在 M3 从持久结果重读；批准状态本身不能证明是否已创建 Task。
func (c *WorkflowsController) persistedMaterialization(w http.ResponseWriter, r *http.Request, request domain.WorkflowTaskRequest, fallback domain.WorkflowTaskRequestMaterialization) (domain.WorkflowTaskRequestMaterialization, bool) {
	if c.Materializations == nil || request.State != domain.WorkflowTaskRequestStateApproved {
		return fallback, true
	}
	_, found, err := c.Materializations.GetWorkflowTaskMaterialization(r.Context(), request.WorkflowID, request.ID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return "", false
	}
	if found {
		return domain.WorkflowTaskRequestMaterializationMaterialized, true
	}
	return domain.WorkflowTaskRequestMaterializationPending, true
}

// decideStatus maps a materialization to the status a client must act on.
//
// 202 Accepted for a pending approval is the whole point of the tri-state: the decision was
// recorded and nothing was created. Answering 200 would let a client that keys on the status
// alone conclude that scheduling happened, and answering 501 (the previous behaviour) called a
// successful, durable decision an unimplemented operation. A rejection is an ordinary 200, and a
// truly materialized approval is a 200 as well — that case is unreachable in M2 and is mapped
// here rather than special-cased away, so M3 does not have to rediscover the rule.
func decideStatus(materialization domain.WorkflowTaskRequestMaterialization) int {
	if materialization == domain.WorkflowTaskRequestMaterializationPending {
		return http.StatusAccepted
	}
	return http.StatusOK
}

// materializationFor derives the materialization of a stored decision.
//
// It reads the persisted request state, not the caller's intent: approved maps to pending
// because no task-creation path exists in this milestone, a rejection has nothing to create,
// and a still-pending row has no decision at all (it is only reachable through a stale read, and
// reporting not_applicable there is the truthful "no task will come from this call").
func materializationFor(state domain.WorkflowTaskRequestState) domain.WorkflowTaskRequestMaterialization {
	switch state {
	case domain.WorkflowTaskRequestStateApproved:
		return domain.WorkflowTaskRequestMaterializationPending
	default:
		return domain.WorkflowTaskRequestMaterializationNotApplicable
	}
}

// decisionMaterialization resolves the status to report, and refuses a contradiction.
//
// The coordinator's status is authoritative when it is valid and points in the same direction as
// the stored decision: an approval may be pending or materialized, a rejection may only be
// not_applicable. Anything else (empty, unknown, or a status that contradicts the stored state)
// is repaired from the stored state rather than published, because a response that said
// "materialized" next to a rejection — or "not_applicable" next to an approval — would be the
// exact ambiguity the tri-state was added to remove.
func decisionMaterialization(
	decision domain.WorkflowTaskRequestDecision,
) domain.WorkflowTaskRequestMaterialization {
	stored := materializationFor(decision.Request.State)
	if !decision.Materialization.Valid() {
		return stored
	}
	approved := decision.Request.State == domain.WorkflowTaskRequestStateApproved
	if approved && decision.Materialization == domain.WorkflowTaskRequestMaterializationNotApplicable {
		return stored
	}
	if !approved && decision.Materialization != domain.WorkflowTaskRequestMaterializationNotApplicable {
		return stored
	}
	return decision.Materialization
}

// decideMessage is the plain-language sentence a client can show without interpretation.
func decideMessage(materialization domain.WorkflowTaskRequestMaterialization) string {
	switch materialization {
	case domain.WorkflowTaskRequestMaterializationPending:
		return "The decision was recorded. No task was created. A separate operator approval is required to materialize the task."
	case domain.WorkflowTaskRequestMaterializationMaterialized:
		return "The decision was recorded and a task was created."
	default:
		return "The decision was recorded."
	}
}

// replayedDecision recognises a retry of the decision that was already applied.
//
// The store's decide is a revision+state CAS: a second call with the same expected revision
// matches no row and reports a conflict. That is right for state, but it is not what a client
// that lost the response needs — it needs the stored decision, so its retry converges.
//
// The replay is recognised narrowly, so a genuine second decision cannot hide behind it:
//
//   - the stored request must already be decided in the SAME direction the retry asks for, and
//   - the stored revision must be exactly one past the retry's expected revision, which is the
//     row the retry was originally written against.
//
// Anything else stays a conflict. Returning a stored decision does not append an audit row: no
// new decision was taken.
func replayedDecision(
	prior domain.WorkflowTaskRequest,
	req DecideWorkflowTaskRequestRequest,
) (domain.WorkflowTaskRequest, bool) {
	if prior.State != domain.WorkflowTaskRequestStateApproved &&
		prior.State != domain.WorkflowTaskRequestStateRejected {
		return domain.WorkflowTaskRequest{}, false
	}
	if prior.Revision != req.ExpectedRevision+1 {
		return domain.WorkflowTaskRequest{}, false
	}
	if req.Approve != (prior.State == domain.WorkflowTaskRequestStateApproved) {
		return domain.WorkflowTaskRequest{}, false
	}
	return prior, true
}

// workflowCapabilityResponse projects one graded snapshot.
func workflowCapabilityResponse(
	snapshot domain.HarnessCapabilitySnapshot,
	now time.Time,
) WorkflowCapabilityResponse {
	models := make([]WorkflowCapabilityModelResponse, 0, len(snapshot.Models))
	for _, choice := range snapshot.Models {
		models = append(models, WorkflowCapabilityModelResponse{
			Model: choice.Model, Effort: choice.Effort, DefaultEffort: choice.Default,
		})
	}
	// A capability matrix is what the UI uses to decide which CONTROLS to offer, so a field that
	// can never authorize a control must not read "supported". Static and local_probe evidence is
	// display-only: capability.Gate refuses every feature on it, and a matrix that advertised
	// steer=supported beside a source that cannot gate would make every client offer a button
	// whose only possible outcome is a 409. The gated controls are therefore reported as unknown
	// for a non-gating source, while the genuinely informational fields (surfaces, streaming,
	// tools, models, usage) keep describing what the provider is known to do.
	gated := func(state domain.CapabilityState) string {
		if snapshot.Source.AllowsSessionGating() {
			return string(state)
		}
		return string(domain.CapabilityStateUnknown)
	}
	resume := string(snapshot.Resume)
	approval := string(snapshot.Approval)
	if !snapshot.Source.AllowsSessionGating() {
		resume = string(domain.CapabilityResumeUnknown)
		approval = string(domain.CapabilityApprovalUnknown)
	}
	return WorkflowCapabilityResponse{
		Harness:           string(snapshot.Harness),
		Source:            string(snapshot.Source),
		AuthState:         string(snapshot.AuthState),
		Readiness:         string(snapshot.Readiness),
		Surfaces:          string(snapshot.Surfaces),
		Streaming:         string(snapshot.Streaming),
		Tools:             string(snapshot.Tools),
		Interrupt:         gated(snapshot.Interrupt),
		Steer:             gated(snapshot.Steer),
		Queue:             gated(snapshot.Queue),
		Resume:            resume,
		Approval:          approval,
		ApplyAt:           string(snapshot.ApplyAt),
		Usage:             string(snapshot.Usage),
		Models:            models,
		ObservedSessionID: string(snapshot.ObservedSessionID),
		Generation:        snapshot.Generation,
		ObservedAt:        snapshot.ObservedAt,
		ExpiresAt:         snapshot.ExpiresAt,
		Expired:           snapshot.Expired(now),
		ReasonCode:        snapshot.ReasonCode,
	}
}

// workflowBindingResponse projects one binding. It projects no lease field by construction:
// the response type has none, and the projection is field-explicit so adding one is a compile
// error rather than a silent leak.
func workflowBindingResponse(
	binding domain.WorkflowAttemptBinding,
	repos []domain.WorkflowAttemptRepoBinding,
) WorkflowAttemptBindingResponse {
	repositories := make([]WorkflowAttemptRepoBindingResponse, 0, len(repos))
	for _, repo := range repos {
		repositories = append(repositories, WorkflowAttemptRepoBindingResponse{
			RepoKey: string(repo.RepoKey), ProjectID: string(repo.ProjectID),
			ExpectedBaseSHA: repo.ExpectedBaseSHA, SessionWorktreeRef: repo.SessionWorktreeRef,
			ObservedHeadSHA: repo.ObservedHeadSHA, State: string(repo.State), Revision: repo.Revision,
		})
	}
	response := WorkflowAttemptBindingResponse{
		AttemptID: string(binding.AttemptID), WorkflowID: string(binding.WorkflowID),
		TaskID: string(binding.TaskID), SpawnRequestID: binding.SpawnRequestID,
		State: string(binding.State), ControllerGeneration: binding.ControllerGeneration,
		BoundAt: binding.BoundAt, FailureCode: binding.FailureCode,
		Revision: binding.Revision, CreatedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
		Repositories: repositories,
	}
	if binding.SessionID != nil {
		response.SessionID = string(*binding.SessionID)
	}
	return response
}

// workflowInterventionResponse projects one intervention receipt.
func workflowInterventionResponse(intervention domain.WorkflowIntervention) WorkflowInterventionResponse {
	response := WorkflowInterventionResponse{
		ID: intervention.ID, WorkflowID: string(intervention.WorkflowID),
		TaskID: string(intervention.TaskID), Mode: string(intervention.Mode),
		State: string(intervention.State), ControllerGeneration: intervention.ControllerGeneration,
		ClientMessageID:      intervention.ClientMessageID,
		TargetProviderTurnID: intervention.TargetProviderTurnID,
		ChatTurnID:           intervention.ChatTurnID, ProviderTurnID: intervention.ProviderTurnID,
		ReasonCode: intervention.ReasonCode, Revision: intervention.Revision,
		CreatedAt: intervention.CreatedAt, UpdatedAt: intervention.UpdatedAt,
		SettledAt: intervention.SettledAt,
	}
	if intervention.AttemptID != nil {
		response.AttemptID = string(*intervention.AttemptID)
	}
	if intervention.SessionID != nil {
		response.SessionID = string(*intervention.SessionID)
	}
	return response
}

// workflowMessageResponse projects one collaboration message.
func workflowMessageResponse(message domain.WorkflowMessage) WorkflowMessageResponse {
	response := WorkflowMessageResponse{
		ID: message.ID, WorkflowID: string(message.WorkflowID),
		FromTaskID: string(message.FromTaskID), SenderKind: string(message.SenderKind),
		Kind: string(message.Kind), DeliveryState: string(message.DeliveryState),
		Body: message.Body, PayloadRef: message.PayloadRef,
		ClientMessageID: message.ClientMessageID, Revision: message.Revision,
		CreatedAt: message.CreatedAt, UpdatedAt: message.UpdatedAt,
	}
	if message.ToTaskID != nil {
		response.ToTaskID = string(*message.ToTaskID)
	}
	return response
}

// listTaskRequests returns one page of a workflow's task requests, each with its redacted
// summary.
//
// It is a GET, so it is reachable without a body: this is the surface an owner uses to find what
// is waiting on them. Every item is the DETAIL shape, whose content is the summary projection and
// never the stored JSON.
func (c *WorkflowsController) listTaskRequests(w http.ResponseWriter, r *http.Request) {
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	if _, ok := c.caller(w, r, principal.RequestFacts{WorkflowID: workflowID}); !ok {
		return
	}
	if c.Collaboration == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/workflows/{workflowId}/task-requests")
		return
	}
	limit, offset, code, message := workflowPage(r)
	if code != "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", code, message, nil)
		return
	}
	items, err := c.Collaboration.ListTaskRequests(r.Context(), workflowID, limit, offset)
	if err != nil {
		envelope.WriteError(w, r, collaborationError(err))
		return
	}
	requests := make([]WorkflowTaskRequestDetailResponse, 0, len(items))
	for _, item := range items {
		detail, detailErr := workflowTaskRequestDetailResponse(item)
		if detailErr != nil {
			envelope.WriteError(w, r, detailErr)
			return
		}
		requests = append(requests, detail)
	}
	envelope.WriteJSON(w, http.StatusOK, ListWorkflowTaskRequestsResponse{
		Requests:   requests,
		NextCursor: workflowNextCursor(offset, len(items), limit),
	})
}

// taskRequest returns one request with its redacted summary.
//
// The lookup is scoped to the URL's workflow, so a request id from elsewhere is a 404 that does
// not distinguish "absent" from "belongs to another workflow".
func (c *WorkflowsController) taskRequest(w http.ResponseWriter, r *http.Request) {
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	if _, ok := c.caller(w, r, principal.RequestFacts{WorkflowID: workflowID}); !ok {
		return
	}
	if c.Collaboration == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/workflows/{workflowId}/task-requests/{requestId}")
		return
	}
	request, ok := c.requireTaskRequestInWorkflow(w, r, workflowID, chi.URLParam(r, "requestId"))
	if !ok {
		return
	}
	detail, err := workflowTaskRequestDetailResponse(request)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowTaskRequestDetailEnvelope{Request: detail})
}

// requireTaskRequestInWorkflow refuses when the request id does not resolve inside the URL's
// workflow.
//
// The lookup is scoped, but the RETURNED row is checked again: a store that dropped its scope
// clause would otherwise let a request from another workflow be read or decided through this URL,
// and the check that catches that has to live where the URL is interpreted. It answers 404 without
// distinguishing "absent" from "belongs elsewhere", because confirming existence elsewhere is the
// disclosure a cross-workflow probe is after.
func (c *WorkflowsController) requireTaskRequestInWorkflow(
	w http.ResponseWriter,
	r *http.Request,
	workflowID domain.WorkflowID,
	requestID string,
) (domain.WorkflowTaskRequest, bool) {
	request, err := c.Collaboration.GetTaskRequest(r.Context(), workflowID, requestID)
	if err != nil {
		envelope.WriteError(w, r, collaborationError(err))
		return domain.WorkflowTaskRequest{}, false
	}
	if request.WorkflowID != workflowID {
		envelope.WriteError(w, r, apierr.NotFound("WORKFLOW_REQUEST_NOT_FOUND", "No such task request"))
		return domain.WorkflowTaskRequest{}, false
	}
	return request, true
}

// workflowTaskRequestDetailResponse projects one request plus its redacted summary.
//
// The redaction lives in collaboration, where it is tested against hostile payloads; this
// function only maps. A redaction failure is an internal error, never an empty summary: an owner
// must not be shown "nothing was requested" because the stored row could not be read.
func workflowTaskRequestDetailResponse(
	request domain.WorkflowTaskRequest,
) (WorkflowTaskRequestDetailResponse, error) {
	summary, err := collaborationsvc.RedactTaskRequest(request)
	if err != nil {
		return WorkflowTaskRequestDetailResponse{}, apierr.InternalCause("WORKFLOW_REQUEST_UNREADABLE",
			"The stored task request could not be summarised", err)
	}
	return WorkflowTaskRequestDetailResponse{
		WorkflowTaskRequestResponse: workflowTaskRequestResponse(request),
		Summary: WorkflowTaskRequestSummary{
			TaskCount:        summary.TaskCount,
			TitleHints:       summary.TitleHints,
			Repos:            summary.Repos,
			BudgetDelta:      summary.BudgetDelta,
			ConcurrencyDelta: summary.ConcurrencyDelta,
			NotesRef:         summary.NotesRef,
		},
	}, nil
}

// recordDecision appends one sensitive decision to the audit trail.
//
// It returns false only when a WIRED recorder rejected the record: a configured recorder that
// cannot persist is a failure the caller must see, because the alternative is telling an operator
// a decision happened while the trail says nothing. With no recorder wired there is nothing to
// append to, and that configuration is reported once at wiring time rather than per request.
//
// Every field is derived: the actor from the proven caller, the subject from the route, the
// request id from the daemon's own middleware, and the token fingerprint from the credential the
// caller proved with. Nothing here can be supplied by a request body.
func (c *WorkflowsController) recordDecision(r *http.Request, record principal.AuditRecord) bool {
	if c.Audit == nil {
		return true
	}
	record.DaemonRequestID = middleware.GetReqID(r.Context())
	record.At = c.now()
	if err := c.Audit.RecordSensitiveDecision(r.Context(), record); err != nil {
		return false
	}
	return true
}

// operatorDecidedByRef derives the recorded decider reference from the proven caller.
//
// It is server-derived so a decision can only ever be attributed to the operator that made it.
// The value names the surface, not a person: loopback has no authenticated user, and inventing a
// username here would be a claim the daemon cannot support.
func operatorDecidedByRef(caller principal.Caller) string {
	if caller.Kind == "" {
		return "operator:local"
	}
	return "operator:" + string(caller.Kind)
}

// workflowTaskRequestResponse projects one task request.
func workflowTaskRequestResponse(request domain.WorkflowTaskRequest) WorkflowTaskRequestResponse {
	response := WorkflowTaskRequestResponse{
		ID: request.ID, WorkflowID: string(request.WorkflowID),
		RequesterTaskID: string(request.RequesterTaskID),
		RequestKind:     string(request.RequestKind), RequestSHA: request.RequestSHA,
		State: string(request.State), DecidedByRef: request.DecidedByRef,
		DecidedAt: request.DecidedAt, Reason: request.Reason,
		WorkflowRevision: request.WorkflowRevision, Revision: request.Revision,
		CreatedAt: request.CreatedAt, UpdatedAt: request.UpdatedAt,
	}
	if request.DecidedByKind != nil {
		response.DecidedByKind = string(*request.DecidedByKind)
	}
	return response
}

// bindError maps a bind failure onto the API error envelope.
//
// Every branch is a typed refusal with its own code, so a client never has to infer the
// situation from a status alone.
func bindError(err error) error {
	switch {
	case errors.Is(err, executionsvc.ErrAttemptNotFound):
		return apierr.NotFound("WORKFLOW_ATTEMPT_NOT_FOUND", "No such attempt in this workflow")
	case errors.Is(err, executionsvc.ErrTaskNotFound):
		return apierr.NotFound("WORKFLOW_TASK_NOT_FOUND", "No such task in this workflow")
	case errors.Is(err, executionsvc.ErrProjectUnresolved):
		return apierr.Conflict("WORKFLOW_PROJECT_UNRESOLVED",
			"No project can be derived for that attempt's task", nil)
	case errors.Is(err, executionsvc.ErrWorkflowMismatch):
		// Checked before the generic binding conflicts so a cross-workflow attempt reports the
		// ownership answer rather than an unrelated transition refusal.
		return apierr.Conflict("WORKFLOW_BIND_CONFLICT",
			"That attempt or task does not belong to this workflow", nil)
	case errors.Is(err, ports.ErrSpawnOutcomeUnknown):
		return apierr.Conflict("SPAWN_OUTCOME_UNKNOWN",
			"The spawn was issued and its outcome cannot be established; it will not be retried", nil)
	case errors.Is(err, ports.ErrExecutionTerminated):
		// The same code and status sessions.go uses for a terminated session, so a client has one
		// spelling for the condition rather than two. TERMINATED is distinct from NOT_READY: the
		// session will never accept work again, so retrying is pointless.
		return apierr.Conflict("SESSION_TERMINATED", "The session is terminated", nil)
	case errors.Is(err, ports.ErrExecutionStaleGeneration):
		return apierr.Conflict("TURN_MISMATCH", "The controller generation has changed", nil)
	case errors.Is(err, ports.ErrExecutionTargetMismatch):
		return apierr.Conflict("TURN_MISMATCH", "That turn does not belong to this session", nil)
	case errors.Is(err, ports.ErrExecutionSessionMismatch):
		return apierr.Conflict("SESSION_MISMATCH", "That session is not this attempt's session", nil)
	case errors.Is(err, ports.ErrExecutionRefused):
		return apierr.Conflict("SESSION_NOT_READY", "The session cannot accept this bind yet", nil)
	case errors.Is(err, ports.ErrExecutionUnsupported):
		return apierr.NotImplemented("CAPABILITY_UNKNOWN", "This daemon cannot perform the requested execution")
	case errors.Is(err, domain.ErrWorkflowBindingRevisionConflict):
		return apierr.Conflict("WORKFLOW_BIND_CONFLICT", "The binding changed; re-read it and retry", nil)
	case errors.Is(err, domain.ErrWorkflowBindingSpawningRequestConflict):
		return apierr.Conflict("WORKFLOW_REQUEST_CONFLICT", "That request id belongs to a different spawn", nil)
	case errors.Is(err, domain.ErrWorkflowBindingSessionTaken):
		return apierr.Conflict("WORKFLOW_BIND_CONFLICT", "The session already belongs to another attempt", nil)
	case errors.Is(err, domain.ErrWorkflowBindingNotFound):
		return apierr.NotFound("WORKFLOW_BINDING_NOT_FOUND", "No binding exists for this attempt")
	case errors.Is(err, domain.ErrInvalidWorkflowBindingTransition),
		errors.Is(err, domain.ErrWorkflowBindingSessionRequired):
		return apierr.Conflict("WORKFLOW_BIND_CONFLICT", "That binding transition is not legal", nil)
	}
	return schedulerError(err)
}

// bindReasonCode maps a frozen bind reason onto a stable client code.
func bindReasonCode(reason string) string {
	switch reason {
	case "spawn_in_progress":
		return "SPAWN_OUTCOME_UNKNOWN"
	case "spawn_unknown", "spawn_no_session", "spawn_unclassified":
		return "SPAWN_OUTCOME_UNKNOWN"
	case "session_readiness_unknown", "session_readiness_unrecognized", "session_exited", "spawn_failed":
		return "SESSION_NOT_READY"
	case "reconcile_lookup_failed", "reconcile_no_session":
		return "INTERVENTION_UNCERTAIN"
	default:
		if reason == "" {
			return "WORKFLOW_BIND_CONFLICT"
		}
		return "WORKFLOW_BIND_CONFLICT"
	}
}

// InterventionReasonCode maps a refusal reason onto the stable client code.
//
// It is exported so the mapping can be pinned directly as a table: the reason strings are
// produced by packages this one does not own, and a new one falling through to a generic code is
// exactly the kind of drift a wire test alone would miss.
func InterventionReasonCode(reason string) string {
	return interventionReasonCode(reason)
}

// interventionError maps an intervention failure onto the API error envelope.
func interventionError(err error) error {
	switch {
	case errors.Is(err, domain.ErrInterventionOperationConflict):
		return apierr.Conflict("WORKFLOW_REQUEST_CONFLICT",
			"That client message id belongs to a different request", nil)
	case errors.Is(err, domain.ErrInterventionEvidenceMissing):
		return apierr.Conflict("INTERVENTION_UNCERTAIN",
			"The evidence needed for that receipt state is absent", nil)
	case errors.Is(err, domain.ErrInterventionStaleGeneration):
		return apierr.Conflict("TURN_MISMATCH",
			"The controller generation has changed; the receipt is stale", nil)
	case errors.Is(err, domain.ErrInterventionTurnRequired):
		return apierr.Invalid("TURN_MISMATCH", "A steer must name the provider turn it joins", nil)
	case errors.Is(err, domain.ErrInvalidWorkflowBindingTransition),
		errors.Is(err, domain.ErrInvalidInterventionTransition):
		return apierr.Conflict("INTERVENTION_UNCERTAIN", "That intervention transition is not legal", nil)
	case errors.Is(err, domain.ErrWorkflowBindingNotFound):
		return apierr.NotFound("WORKFLOW_BINDING_NOT_FOUND", "No binding exists for this attempt")
	case errors.Is(err, ports.ErrExecutionTerminated):
		return apierr.Conflict("SESSION_TERMINATED", "The session is terminated", nil)
	case errors.Is(err, ports.ErrExecutionSessionMismatch):
		return apierr.Conflict("SESSION_MISMATCH", "That session is not this attempt's session", nil)
	case errors.Is(err, ports.ErrExecutionRefused):
		return apierr.Conflict("SESSION_NOT_READY", "The session cannot accept this intervention", nil)
	case errors.Is(err, ports.ErrExecutionUnsupported):
		return apierr.Conflict("STEER_UNSUPPORTED",
			"This provider cannot take guidance into a running turn", nil)
	}
	return err
}

// interventionReasonCode maps a refusal reason onto a stable client code.
func interventionReasonCode(reason string) string {
	switch reason {
	case string(ports.CapabilityReasonStaticOnly), ports.CapabilityReasonNotProbed,
		ports.CapabilityReasonSnapshotExpired, ports.CapabilityReasonProbeFailed,
		ports.CapabilityReasonProbeTimeout,
		// not_observed means the handshake completed but simply did not mention this feature, and
		// not_advertised means it explicitly did not. Both are "this provider is not offering
		// steering", which is the fact STEER_UNSUPPORTED states and queueAvailable already
		// answers; falling through to CAPABILITY_UNKNOWN would tell the caller "unknown" when the
		// daemon knows exactly what to advise.
		ports.CapabilityReasonNotObserved, ports.CapabilityReasonNotAdvertised:
		return "STEER_UNSUPPORTED"
	case ports.CapabilityReasonGenerationMismatch:
		// The evidence describes a different controller generation: the turn the caller meant is
		// no longer addressable, which is exactly what TURN_MISMATCH means.
		return "TURN_MISMATCH"
	case ports.CapabilityReasonSessionMismatch:
		// Harness-level evidence cannot authorize a per-session feature. This is NOT a stale
		// turn: the daemon holds no per-session steer evidence at all, so the honest code is the
		// same one a provider that cannot steer produces — and the caller is offered the queue.
		return "STEER_UNSUPPORTED"
	case ports.CapabilityReasonNoSession:
		return "TURN_MISMATCH"
	case "session_not_live", "binding_not_live":
		return "SESSION_NOT_READY"
	default:
		if reason == "" {
			return "INTERVENTION_UNCERTAIN"
		}
		return "CAPABILITY_UNKNOWN"
	}
}

// collaborationError maps a collaboration failure onto the API error envelope.
func collaborationError(err error) error {
	switch {
	case errors.Is(err, domain.ErrCollaborationCrossWorkflow):
		return apierr.Conflict("COLLAB_NOT_AUTHORIZED", "That reference belongs to another workflow", nil)
	case errors.Is(err, domain.ErrCollaborationNotAuthorized):
		return apierr.Forbidden("COLLAB_NOT_AUTHORIZED", "The caller may not act for that task")
	case errors.Is(err, domain.ErrCollaborationDeciderNotPermitted):
		return apierr.Forbidden("COLLAB_NOT_AUTHORIZED", "Only a human or the daemon may decide")
	case errors.Is(err, domain.ErrWorkflowMessageConflict):
		return apierr.Conflict("COLLAB_MESSAGE_CONFLICT",
			"That client message id belongs to a different message", nil)
	case errors.Is(err, domain.ErrWorkflowMessageQueueFull):
		return apierr.TooManyRequests("COLLAB_QUEUE_FULL",
			"The workflow has too many open messages")
	case errors.Is(err, domain.ErrWorkflowTaskRequestConflict):
		return apierr.Conflict("WORKFLOW_REQUEST_CONFLICT",
			"That request id belongs to a different request, or it was already decided", nil)
	case errors.Is(err, domain.ErrWorkflowTaskRequestTampered):
		return apierr.Conflict("WORKFLOW_REQUEST_TAMPERED",
			"The stored request no longer matches its fingerprint", nil)
	case errors.Is(err, domain.ErrWorkflowTaskRequestGraphMoved):
		return apierr.Conflict("WORKFLOW_GRAPH_MOVED",
			"The workflow changed since the request was raised; it must be re-raised", nil)
	case errors.Is(err, domain.ErrWorkflowTaskRequestNotFound):
		return apierr.NotFound("WORKFLOW_REQUEST_NOT_FOUND", "No such task request")
	case errors.Is(err, domain.ErrWorkflowMessageNotFound):
		return apierr.NotFound("WORKFLOW_MESSAGE_NOT_FOUND", "No such message")
	case errors.Is(err, domain.ErrCollaborationWorkflowNotFound):
		return apierr.NotFound("WORKFLOW_NOT_FOUND", "No such workflow")
	case errors.Is(err, domain.ErrInvalidWorkflowMessage),
		errors.Is(err, domain.ErrInvalidWorkflowMessageSender),
		errors.Is(err, domain.ErrInvalidWorkflowMessageKind),
		errors.Is(err, domain.ErrInvalidWorkflowMessageTarget),
		errors.Is(err, domain.ErrInvalidWorkflowMessageContent),
		errors.Is(err, domain.ErrInvalidWorkflowMessageDelivery),
		errors.Is(err, domain.ErrInvalidWorkflowTaskRequest),
		errors.Is(err, domain.ErrInvalidWorkflowTaskRequestKind),
		errors.Is(err, domain.ErrInvalidWorkflowTaskRequestPayload),
		errors.Is(err, domain.ErrInvalidWorkflowTaskRequestDecision):
		return apierr.Invalid("WORKFLOW_MESSAGE_INVALID", "The request body is not valid", nil)
	}
	return schedulerError(err)
}
