package workflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Store is the narrow persistence surface required by Workflow use cases.
type Store interface {
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	GetWorkflowByIdempotencyKey(context.Context, string, string) (domain.Workflow, bool, error)
	CreateWorkflow(context.Context, domain.Workflow, []domain.WorkflowRepository, []domain.WorkflowTask, []domain.WorkflowEdge, []domain.WorkflowTaskRepository, string, string) (domain.Workflow, bool, error)
	GetWorkflow(context.Context, domain.WorkflowID) (domain.Workflow, bool, error)
	ListWorkflows(context.Context, domain.WorkflowState, int64, int64) ([]domain.Workflow, error)
	GetWorkflowTask(context.Context, domain.WorkflowID, domain.WorkflowTaskID) (domain.WorkflowTask, bool, error)
	ListWorkflowTasks(context.Context, domain.WorkflowID, domain.WorkflowTaskState, int64, int64) ([]domain.WorkflowTask, error)
	ListWorkflowRepositories(context.Context, domain.WorkflowID) ([]domain.WorkflowRepository, error)
	ListWorkflowEdges(context.Context, domain.WorkflowID) ([]domain.WorkflowEdge, error)
	ListWorkflowTaskRepositories(context.Context, domain.WorkflowID, domain.WorkflowTaskID) ([]domain.WorkflowTaskRepository, error)
	ListWorkflowAttempts(context.Context, domain.WorkflowID, domain.WorkflowTaskID) ([]domain.WorkflowAttempt, error)
	ActivateWorkflow(context.Context, domain.WorkflowID, int64, string, string, time.Time) (domain.Workflow, bool, error)
	ClaimWorkflowTask(context.Context, domain.WorkflowAttempt, int64, string, string) (domain.WorkflowAttempt, bool, error)
	StartWorkflowAttempt(context.Context, domain.WorkflowID, domain.WorkflowAttemptID, string, string, string, time.Time) (domain.WorkflowAttempt, bool, error)
	SettleWorkflowAttempt(context.Context, domain.WorkflowID, domain.WorkflowAttemptID, string, domain.WorkflowAttemptState, string, string, string, time.Time) (domain.WorkflowAttempt, bool, error)
	ResolveUnknownWorkflowAttempt(context.Context, domain.WorkflowID, domain.WorkflowAttemptID, domain.WorkflowAttemptState, string, int64, string, string, time.Time) (domain.WorkflowAttempt, bool, error)
	RetryWorkflowTask(context.Context, domain.WorkflowID, domain.WorkflowTaskID, int64, string, string, time.Time) (domain.WorkflowTask, bool, error)
	RecoverExpiredWorkflowAttempts(context.Context, time.Time) (int64, error)
}

// Deps contains injectable clock, identity, graph-bound, and lease settings.
type Deps struct {
	Store         Store
	Clock         func() time.Time
	NewID         func() string
	NewLeaseToken func() (string, error)
	GraphLimits   domain.WorkflowGraphLimits
	LeaseDuration time.Duration
}

// Service owns Workflow validation and lifecycle orchestration. It never
// launches an Agent, invokes a provider, or runs Git commands.
type Service struct {
	store         Store
	clock         func() time.Time
	newID         func() string
	newLeaseToken func() (string, error)
	graphLimits   domain.WorkflowGraphLimits
	leaseDuration time.Duration
}

// New constructs a Workflow service. Graph limits and lease duration remain
// explicit deployment settings rather than hidden policy defaults.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.NewLeaseToken == nil {
		deps.NewLeaseToken = randomLeaseToken
	}
	return &Service{
		store: deps.Store, clock: deps.Clock, newID: deps.NewID,
		newLeaseToken: deps.NewLeaseToken, graphLimits: deps.GraphLimits,
		leaseDuration: deps.LeaseDuration,
	}
}

// RepositoryInput binds a workflow-local repository key to a registered project.
type RepositoryInput struct {
	RepoKey   domain.WorkflowRepoKey `json:"repoKey"`
	ProjectID domain.ProjectID       `json:"projectId"`
	PathRef   string                 `json:"pathRef,omitempty"`
	BaseSHA   string                 `json:"baseSha,omitempty"`
	TargetRef string                 `json:"targetRef,omitempty"`
}

// TaskInput describes a task without allowing the caller to forge lifecycle state.
type TaskInput struct {
	ID           domain.WorkflowTaskID   `json:"id"`
	Kind         domain.WorkflowTaskKind `json:"kind"`
	Title        string                  `json:"title"`
	Brief        string                  `json:"brief,omitempty"`
	Priority     int64                   `json:"priority,omitempty"`
	AgentHarness domain.AgentHarness     `json:"agentHarness,omitempty"`
	MaxAttempts  int64                   `json:"maxAttempts,omitempty"`
}

// DependencyInput declares that FromTaskID must succeed before ToTaskID is ready.
type DependencyInput struct {
	FromTaskID domain.WorkflowTaskID `json:"fromTaskId"`
	ToTaskID   domain.WorkflowTaskID `json:"toTaskId"`
}

// TaskRepositoryInput scopes a task to one workflow repository.
type TaskRepositoryInput struct {
	TaskID       domain.WorkflowTaskID  `json:"taskId"`
	RepoKey      domain.WorkflowRepoKey `json:"repoKey"`
	AllowedPaths []string               `json:"allowedPaths,omitempty"`
	BaseSHA      string                 `json:"baseSha,omitempty"`
}

// CreateInput is the user-authored definition; execution state is assigned by the service.
type CreateInput struct {
	Title            string                `json:"title"`
	Goal             string                `json:"goal"`
	OwnerSessionID   *domain.SessionID     `json:"ownerSessionId,omitempty"`
	Repositories     []RepositoryInput     `json:"repositories"`
	Tasks            []TaskInput           `json:"tasks"`
	Dependencies     []DependencyInput     `json:"dependencies,omitempty"`
	TaskRepositories []TaskRepositoryInput `json:"taskRepositories,omitempty"`
}

// ClaimInput identifies the ready task and the worker requesting its lease.
type ClaimInput struct {
	WorkflowID       domain.WorkflowID
	TaskID           domain.WorkflowTaskID
	ExpectedRevision int64
	WorkerID         string
	IdempotencyKey   string
}

// ClaimResult returns the persisted attempt plus the one-time lease capability.
type ClaimResult struct {
	Attempt    domain.WorkflowAttempt `json:"attempt"`
	LeaseToken string                 `json:"leaseToken"`
}

// Definition is one workflow with its complete persisted DAG. It is the read
// model behind the workflow detail endpoint; execution state lives in Tasks
// and each task's Attempt history.
type Definition struct {
	Workflow         domain.Workflow                 `json:"workflow"`
	Repositories     []domain.WorkflowRepository     `json:"repositories"`
	Tasks            []domain.WorkflowTask           `json:"tasks"`
	Edges            []domain.WorkflowEdge           `json:"edges"`
	TaskRepositories []domain.WorkflowTaskRepository `json:"taskRepositories"`
}

// StartInput supplies the capability token issued when the attempt was claimed.
type StartInput struct {
	WorkflowID     domain.WorkflowID
	AttemptID      domain.WorkflowAttemptID
	LeaseToken     string
	IdempotencyKey string
}

// SettleInput records a worker's terminal result without replaying the worker.
type SettleInput struct {
	WorkflowID     domain.WorkflowID
	AttemptID      domain.WorkflowAttemptID
	LeaseToken     string
	Result         domain.WorkflowAttemptState
	ErrorCode      string
	IdempotencyKey string
}

// RetryInput reopens a retry_wait task after a failed attempt.
type RetryInput struct {
	WorkflowID       domain.WorkflowID
	TaskID           domain.WorkflowTaskID
	ExpectedRevision int64
	IdempotencyKey   string
}

// ResolveInput records the decided outcome of an attempt whose lease expired.
// ExpectedTaskRevision is the operator's compare-and-swap: two reconcilers
// deciding different outcomes for the same expired attempt cannot both apply.
type ResolveInput struct {
	WorkflowID           domain.WorkflowID
	AttemptID            domain.WorkflowAttemptID
	Outcome              domain.WorkflowAttemptState
	ErrorCode            string
	ExpectedTaskRevision int64
	IdempotencyKey       string
}

// Create validates a complete DAG and atomically persists its definition.
func (s *Service) Create(ctx context.Context, idempotencyKey string, input CreateInput) (domain.Workflow, bool, error) {
	if s.store == nil {
		return domain.Workflow{}, false, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return domain.Workflow{}, false, err
	}
	if s.graphLimits.MaxTasks < 1 || s.graphLimits.MaxDepth < 1 || s.graphLimits.MaxFanOut < 0 {
		return domain.Workflow{}, false, apierr.Internal("WORKFLOW_LIMITS_NOT_CONFIGURED", "Workflow graph limits are not configured")
	}

	input = normalizeCreateInput(input)
	if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Goal) == "" {
		return domain.Workflow{}, false, apierr.Invalid("INVALID_WORKFLOW", "Workflow title and goal are required", nil)
	}
	if len(input.Tasks) == 0 {
		return domain.Workflow{}, false, apierr.Invalid("WORKFLOW_REQUIRES_TASKS", "A workflow must contain at least one task", nil)
	}
	if len(input.Repositories) == 0 {
		return domain.Workflow{}, false, apierr.Invalid("WORKFLOW_REQUIRES_REPOSITORY", "A coding workflow must bind at least one repository", nil)
	}

	now := s.clock().UTC()
	workflowID := domain.WorkflowID("workflow-" + s.newID())
	rec := domain.Workflow{
		ID: workflowID, Title: input.Title, Goal: input.Goal, OwnerSessionID: input.OwnerSessionID,
		State: domain.WorkflowStateDraft, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := rec.Validate(); err != nil {
		return domain.Workflow{}, false, apierr.Invalid("INVALID_WORKFLOW", err.Error(), nil)
	}

	repositories := make([]domain.WorkflowRepository, 0, len(input.Repositories))
	for _, repository := range input.Repositories {
		repositories = append(repositories, domain.WorkflowRepository{
			WorkflowID: workflowID, RepoKey: repository.RepoKey, ProjectID: repository.ProjectID,
			PathRef: repository.PathRef, BaseSHA: repository.BaseSHA, TargetRef: repository.TargetRef,
		})
	}

	tasks := make([]domain.WorkflowTask, 0, len(input.Tasks))
	taskIDs := make([]domain.WorkflowTaskID, 0, len(input.Tasks))
	for _, task := range input.Tasks {
		maxAttempts := task.MaxAttempts
		if maxAttempts == 0 {
			maxAttempts = 1
		}
		if task.AgentHarness != "" && !task.AgentHarness.IsKnown() {
			return domain.Workflow{}, false, apierr.Invalid("INVALID_WORKFLOW_HARNESS", "A task references an unknown Agent harness", nil)
		}
		record := domain.WorkflowTask{
			ID: task.ID, WorkflowID: workflowID, Kind: task.Kind, Title: task.Title, Brief: task.Brief,
			State: domain.WorkflowTaskStatePending, Priority: task.Priority, AgentHarness: task.AgentHarness,
			MaxAttempts: maxAttempts, Revision: 1, CreatedAt: now, UpdatedAt: now,
		}
		if err := record.Validate(); err != nil {
			return domain.Workflow{}, false, apierr.Invalid("INVALID_WORKFLOW_TASK", err.Error(), map[string]any{"taskId": task.ID})
		}
		tasks = append(tasks, record)
		taskIDs = append(taskIDs, task.ID)
	}

	edges := make([]domain.WorkflowEdge, 0, len(input.Dependencies))
	for _, dependency := range input.Dependencies {
		edges = append(edges, domain.WorkflowEdge{
			WorkflowID: workflowID, FromTaskID: dependency.FromTaskID, ToTaskID: dependency.ToTaskID,
		})
	}
	taskRepositories := make([]domain.WorkflowTaskRepository, 0, len(input.TaskRepositories))
	for _, binding := range input.TaskRepositories {
		taskRepositories = append(taskRepositories, domain.WorkflowTaskRepository{
			WorkflowID: workflowID, TaskID: binding.TaskID, RepoKey: binding.RepoKey,
			AllowedPaths: binding.AllowedPaths, BaseSHA: binding.BaseSHA,
		})
	}

	graph := domain.WorkflowGraph{
		WorkflowID: workflowID, TaskIDs: taskIDs, Edges: edges,
		Repositories: repositories, TaskRepositories: taskRepositories,
	}
	if _, err := domain.ValidateWorkflowGraph(graph, s.graphLimits); err != nil {
		var graphErr *domain.WorkflowGraphValidationError
		if errors.As(err, &graphErr) {
			details := map[string]any{"code": string(graphErr.Code), "field": graphErr.FieldPath()}
			if graphErr.TaskID != "" {
				details["taskId"] = graphErr.TaskID
			}
			if graphErr.RepoKey != "" {
				details["repoKey"] = graphErr.RepoKey
			}
			return domain.Workflow{}, false, apierr.Invalid("INVALID_WORKFLOW_GRAPH", graphErr.Error(), details)
		}
		return domain.Workflow{}, false, apierr.Invalid("INVALID_WORKFLOW_GRAPH", err.Error(), nil)
	}

	// Canonicalize only after validation so a path containing a parent segment
	// cannot be normalized away before the traversal check sees it.
	for i := range input.Repositories {
		pathRef, ok := domain.CanonicalWorkflowPath(input.Repositories[i].PathRef, true)
		if !ok {
			return domain.Workflow{}, false, apierr.Invalid("INVALID_WORKFLOW_GRAPH", domain.ErrInvalidWorkflowRepository.Error(), nil)
		}
		input.Repositories[i].PathRef = pathRef
		repositories[i].PathRef = pathRef
	}
	for i := range input.TaskRepositories {
		for j := range input.TaskRepositories[i].AllowedPaths {
			allowedPath, ok := domain.CanonicalWorkflowPath(input.TaskRepositories[i].AllowedPaths[j], false)
			if !ok {
				return domain.Workflow{}, false, apierr.Invalid("INVALID_WORKFLOW_GRAPH", domain.ErrInvalidWorkflowTaskRepository.Error(), nil)
			}
			input.TaskRepositories[i].AllowedPaths[j] = allowedPath
		}
		sort.Strings(input.TaskRepositories[i].AllowedPaths)
		taskRepositories[i].AllowedPaths = append([]string{}, input.TaskRepositories[i].AllowedPaths...)
	}

	fingerprint, err := requestFingerprint(input)
	if err != nil {
		return domain.Workflow{}, false, apierr.InternalCause("WORKFLOW_FINGERPRINT_FAILED", "Failed to identify the workflow request", err)
	}
	existing, found, err := s.store.GetWorkflowByIdempotencyKey(ctx, idempotencyKey, fingerprint)
	if err != nil {
		return domain.Workflow{}, false, mapStoreError("WORKFLOW_CREATE_FAILED", "Failed to create workflow", err)
	}
	if found {
		return existing, false, nil
	}
	for _, repository := range input.Repositories {
		project, found, err := s.store.GetProject(ctx, string(repository.ProjectID))
		if err != nil {
			return domain.Workflow{}, false, apierr.InternalCause("WORKFLOW_PROJECT_LOOKUP_FAILED", "Failed to validate a workflow repository", err)
		}
		if !found || !project.ArchivedAt.IsZero() {
			return domain.Workflow{}, false, apierr.NotFound("WORKFLOW_PROJECT_NOT_FOUND", "A workflow repository references an unknown or archived project")
		}
	}
	created, inserted, err := s.store.CreateWorkflow(
		ctx, rec, repositories, tasks, edges, taskRepositories, idempotencyKey, fingerprint,
	)
	if err != nil {
		return domain.Workflow{}, false, mapStoreError("WORKFLOW_CREATE_FAILED", "Failed to create workflow", err)
	}
	return created, inserted, nil
}

// Get returns one workflow with its persisted definition.
func (s *Service) Get(ctx context.Context, id domain.WorkflowID) (domain.Workflow, error) {
	if s.store == nil {
		return domain.Workflow{}, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	rec, found, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return domain.Workflow{}, apierr.InternalCause("WORKFLOW_READ_FAILED", "Failed to read workflow", err)
	}
	if !found {
		return domain.Workflow{}, apierr.NotFound("WORKFLOW_NOT_FOUND", "Unknown workflow")
	}
	return rec, nil
}

// List returns a bounded page of workflows.
func (s *Service) List(ctx context.Context, state domain.WorkflowState, limit, offset int64) ([]domain.Workflow, error) {
	if s.store == nil {
		return nil, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	if state != "" && !state.Valid() {
		return nil, apierr.Invalid("INVALID_WORKFLOW_STATE", "Unknown workflow state", nil)
	}
	limit, offset, err := normalizePage(limit, offset)
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListWorkflows(ctx, state, limit, offset)
	if err != nil {
		return nil, apierr.InternalCause("WORKFLOW_LIST_FAILED", "Failed to list workflows", err)
	}
	return items, nil
}

// ListTasks returns a bounded page of tasks for one workflow.
func (s *Service) ListTasks(ctx context.Context, workflowID domain.WorkflowID, state domain.WorkflowTaskState, limit, offset int64) ([]domain.WorkflowTask, error) {
	if s.store == nil {
		return nil, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	if state != "" && !state.Valid() {
		return nil, apierr.Invalid("INVALID_WORKFLOW_TASK_STATE", "Unknown workflow task state", nil)
	}
	if _, err := s.Get(ctx, workflowID); err != nil {
		return nil, err
	}
	limit, offset, err := normalizePage(limit, offset)
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListWorkflowTasks(ctx, workflowID, state, limit, offset)
	if err != nil {
		return nil, apierr.InternalCause("WORKFLOW_TASK_LIST_FAILED", "Failed to list workflow tasks", err)
	}
	return items, nil
}

// ListAttempts returns the durable execution history for one task.
func (s *Service) ListAttempts(ctx context.Context, workflowID domain.WorkflowID, taskID domain.WorkflowTaskID) ([]domain.WorkflowAttempt, error) {
	if _, err := s.GetTask(ctx, workflowID, taskID); err != nil {
		return nil, err
	}
	items, err := s.store.ListWorkflowAttempts(ctx, workflowID, taskID)
	if err != nil {
		return nil, apierr.InternalCause("WORKFLOW_ATTEMPT_LIST_FAILED", "Failed to list workflow attempts", err)
	}
	return items, nil
}

// GetTask returns one task scoped to its workflow.
func (s *Service) GetTask(ctx context.Context, workflowID domain.WorkflowID, taskID domain.WorkflowTaskID) (domain.WorkflowTask, error) {
	if s.store == nil {
		return domain.WorkflowTask{}, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	if _, err := s.Get(ctx, workflowID); err != nil {
		return domain.WorkflowTask{}, err
	}
	rec, found, err := s.store.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		return domain.WorkflowTask{}, apierr.InternalCause("WORKFLOW_TASK_READ_FAILED", "Failed to read workflow task", err)
	}
	if !found {
		return domain.WorkflowTask{}, apierr.NotFound("WORKFLOW_TASK_NOT_FOUND", "Unknown workflow task")
	}
	return rec, nil
}

// GetDefinition returns one workflow with every child row that defines its
// graph. A definition must be complete, so tasks are paged rather than capped
// at one page, and a workflow larger than the configured limit is reported
// instead of silently truncated.
func (s *Service) GetDefinition(ctx context.Context, id domain.WorkflowID) (Definition, error) {
	if s.store == nil {
		return Definition{}, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	// Resolving the workflow first keeps a missing workflow distinct from a
	// workflow that merely has no children.
	rec, err := s.Get(ctx, id)
	if err != nil {
		return Definition{}, err
	}
	repositories, err := s.store.ListWorkflowRepositories(ctx, id)
	if err != nil {
		return Definition{}, apierr.InternalCause("WORKFLOW_DEFINITION_FAILED", "Failed to read workflow definition", err)
	}
	edges, err := s.store.ListWorkflowEdges(ctx, id)
	if err != nil {
		return Definition{}, apierr.InternalCause("WORKFLOW_DEFINITION_FAILED", "Failed to read workflow definition", err)
	}

	const taskPageSize int64 = 100
	maxTasks := int64(s.graphLimits.MaxTasks)
	tasks := make([]domain.WorkflowTask, 0)
	for offset := int64(0); ; offset += taskPageSize {
		page, err := s.store.ListWorkflowTasks(ctx, id, "", taskPageSize, offset)
		if err != nil {
			return Definition{}, apierr.InternalCause("WORKFLOW_DEFINITION_FAILED", "Failed to read workflow definition", err)
		}
		tasks = append(tasks, page...)
		// A configured limit bounds the read; with no limit configured the read
		// still stops at the first short page, because Create refuses to persist
		// a graph at all until limits are set.
		if maxTasks >= 1 && int64(len(tasks)) > maxTasks {
			return Definition{}, apierr.Internal("WORKFLOW_DEFINITION_TOO_LARGE", "Workflow definition exceeds the configured task limit")
		}
		if int64(len(page)) < taskPageSize {
			break
		}
	}

	// One query per task is deliberate: a definition read is bounded by the
	// graph limit and an N+1 here costs less than a batching layer to maintain.
	taskRepositories := make([]domain.WorkflowTaskRepository, 0, len(tasks))
	for _, task := range tasks {
		bindings, err := s.store.ListWorkflowTaskRepositories(ctx, id, task.ID)
		if err != nil {
			return Definition{}, apierr.InternalCause("WORKFLOW_DEFINITION_FAILED", "Failed to read workflow definition", err)
		}
		taskRepositories = append(taskRepositories, bindings...)
	}

	// Empty children stay non-nil so the JSON encodes as [] rather than null.
	if repositories == nil {
		repositories = []domain.WorkflowRepository{}
	}
	if edges == nil {
		edges = []domain.WorkflowEdge{}
	}
	return Definition{
		Workflow:         rec,
		Repositories:     repositories,
		Tasks:            tasks,
		Edges:            edges,
		TaskRepositories: taskRepositories,
	}, nil
}

// Activate transitions a draft workflow to active and derives initial READY tasks.
func (s *Service) Activate(ctx context.Context, workflowID domain.WorkflowID, expectedRevision int64, idempotencyKey string) (domain.Workflow, bool, error) {
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return domain.Workflow{}, false, err
	}
	if _, err := s.Get(ctx, workflowID); err != nil {
		return domain.Workflow{}, false, err
	}
	tasks, err := s.store.ListWorkflowTasks(ctx, workflowID, "", 1, 0)
	if err != nil {
		return domain.Workflow{}, false, apierr.InternalCause("WORKFLOW_TASK_LIST_FAILED", "Failed to inspect workflow tasks", err)
	}
	if len(tasks) == 0 {
		return domain.Workflow{}, false, apierr.Invalid("WORKFLOW_REQUIRES_TASKS", "A workflow must contain at least one task before activation", nil)
	}
	fingerprint, err := requestFingerprint(struct {
		WorkflowID       domain.WorkflowID `json:"workflowId"`
		ExpectedRevision int64             `json:"expectedRevision"`
	}{workflowID, expectedRevision})
	if err != nil {
		return domain.Workflow{}, false, apierr.InternalCause("WORKFLOW_FINGERPRINT_FAILED", "Failed to identify the activation request", err)
	}
	rec, applied, err := s.store.ActivateWorkflow(ctx, workflowID, expectedRevision, idempotencyKey, fingerprint, s.clock().UTC())
	if err != nil {
		return domain.Workflow{}, false, mapStoreError("WORKFLOW_ACTIVATE_FAILED", "Failed to activate workflow", err)
	}
	return rec, applied, nil
}

// Claim atomically leases a ready task and creates its first Attempt record.
func (s *Service) Claim(ctx context.Context, input ClaimInput) (ClaimResult, bool, error) {
	if s.store == nil {
		return ClaimResult{}, false, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return ClaimResult{}, false, err
	}
	input.WorkerID = strings.TrimSpace(input.WorkerID)
	if input.WorkerID == "" {
		return ClaimResult{}, false, apierr.Invalid("INVALID_WORKER_ID", "Worker ID is required", nil)
	}
	if s.leaseDuration <= 0 {
		return ClaimResult{}, false, apierr.Internal("WORKFLOW_LEASE_NOT_CONFIGURED", "Workflow lease duration is not configured")
	}
	if _, err := s.GetTask(ctx, input.WorkflowID, input.TaskID); err != nil {
		return ClaimResult{}, false, err
	}

	now := s.clock().UTC()
	leaseUntil := now.Add(s.leaseDuration)
	leaseToken, err := s.newLeaseToken()
	if err != nil {
		return ClaimResult{}, false, apierr.InternalCause("WORKFLOW_LEASE_TOKEN_FAILED", "Failed to issue a workflow lease", err)
	}
	attempt := domain.WorkflowAttempt{
		ID:         domain.WorkflowAttemptID("attempt-" + s.newID()),
		WorkflowID: input.WorkflowID, TaskID: input.TaskID, WorkerID: input.WorkerID,
		LeaseToken: leaseToken, LeaseUntil: &leaseUntil, State: domain.WorkflowAttemptStateClaimed,
		CreatedAt: now, UpdatedAt: now,
	}
	fingerprint, err := requestFingerprint(struct {
		WorkflowID       domain.WorkflowID     `json:"workflowId"`
		TaskID           domain.WorkflowTaskID `json:"taskId"`
		ExpectedRevision int64                 `json:"expectedRevision"`
		WorkerID         string                `json:"workerId"`
	}{input.WorkflowID, input.TaskID, input.ExpectedRevision, input.WorkerID})
	if err != nil {
		return ClaimResult{}, false, apierr.InternalCause("WORKFLOW_FINGERPRINT_FAILED", "Failed to identify the claim request", err)
	}
	rec, created, err := s.store.ClaimWorkflowTask(ctx, attempt, input.ExpectedRevision, input.IdempotencyKey, fingerprint)
	if err != nil {
		return ClaimResult{}, false, mapStoreError("WORKFLOW_CLAIM_FAILED", "Failed to claim workflow task", err)
	}
	// A replayed claim resolves the attempt that the original key created. Once
	// recovery has turned that attempt UNKNOWN it has no lease token left, so
	// returning it with created=false would hand the caller a capability-free
	// token and fail later with an opaque lease conflict. Report the state
	// instead of a usable result.
	if !created && rec.LeaseToken == "" {
		return ClaimResult{}, false, apierr.Conflict(
			"WORKFLOW_CLAIM_REPLAY_EXPIRED",
			"The original claim is no longer active; the task must be reconciled before it can be claimed again",
			nil,
		)
	}
	return ClaimResult{Attempt: rec, LeaseToken: rec.LeaseToken}, created, nil
}

// Start marks a claimed attempt as running while checking its lease capability.
func (s *Service) Start(ctx context.Context, input StartInput) (domain.WorkflowAttempt, bool, error) {
	if s.store == nil {
		return domain.WorkflowAttempt{}, false, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return domain.WorkflowAttempt{}, false, err
	}
	input.LeaseToken = strings.TrimSpace(input.LeaseToken)
	if input.LeaseToken == "" {
		return domain.WorkflowAttempt{}, false, apierr.Invalid("INVALID_WORKFLOW_LEASE", "Lease token is required", nil)
	}
	fingerprint, err := requestFingerprint(struct {
		WorkflowID domain.WorkflowID        `json:"workflowId"`
		AttemptID  domain.WorkflowAttemptID `json:"attemptId"`
		LeaseToken string                   `json:"leaseToken"`
	}{input.WorkflowID, input.AttemptID, input.LeaseToken})
	if err != nil {
		return domain.WorkflowAttempt{}, false, apierr.InternalCause("WORKFLOW_FINGERPRINT_FAILED", "Failed to identify the start request", err)
	}
	rec, applied, err := s.store.StartWorkflowAttempt(
		ctx, input.WorkflowID, input.AttemptID, input.LeaseToken, input.IdempotencyKey,
		fingerprint, s.clock().UTC(),
	)
	if err != nil {
		return domain.WorkflowAttempt{}, false, mapStoreError("WORKFLOW_ATTEMPT_START_FAILED", "Failed to start workflow attempt", err)
	}
	return rec, applied, nil
}

// Settle records the terminal outcome of an attempt and updates its task atomically.
func (s *Service) Settle(ctx context.Context, input SettleInput) (domain.WorkflowAttempt, bool, error) {
	if s.store == nil {
		return domain.WorkflowAttempt{}, false, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return domain.WorkflowAttempt{}, false, err
	}
	input.LeaseToken = strings.TrimSpace(input.LeaseToken)
	input.ErrorCode = strings.TrimSpace(input.ErrorCode)
	if input.LeaseToken == "" {
		return domain.WorkflowAttempt{}, false, apierr.Invalid("INVALID_WORKFLOW_LEASE", "Lease token is required", nil)
	}
	if input.Result != domain.WorkflowAttemptStateSucceeded && input.Result != domain.WorkflowAttemptStateFailed {
		return domain.WorkflowAttempt{}, false, apierr.Invalid("INVALID_ATTEMPT_RESULT", "Attempt result must be succeeded or failed", nil)
	}
	if len(input.ErrorCode) > 256 {
		return domain.WorkflowAttempt{}, false, apierr.Invalid("INVALID_ATTEMPT_ERROR_CODE", "Attempt error code is too long", nil)
	}
	fingerprint, err := requestFingerprint(struct {
		WorkflowID domain.WorkflowID           `json:"workflowId"`
		AttemptID  domain.WorkflowAttemptID    `json:"attemptId"`
		LeaseToken string                      `json:"leaseToken"`
		Result     domain.WorkflowAttemptState `json:"result"`
		ErrorCode  string                      `json:"errorCode"`
	}{input.WorkflowID, input.AttemptID, input.LeaseToken, input.Result, input.ErrorCode})
	if err != nil {
		return domain.WorkflowAttempt{}, false, apierr.InternalCause("WORKFLOW_FINGERPRINT_FAILED", "Failed to identify the settle request", err)
	}
	rec, applied, err := s.store.SettleWorkflowAttempt(
		ctx, input.WorkflowID, input.AttemptID, input.LeaseToken, input.Result, input.ErrorCode,
		input.IdempotencyKey, fingerprint, s.clock().UTC(),
	)
	if err != nil {
		return domain.WorkflowAttempt{}, false, mapStoreError("WORKFLOW_ATTEMPT_SETTLE_FAILED", "Failed to settle workflow attempt", err)
	}
	return rec, applied, nil
}

// Resolve records an operator's decided outcome for an attempt whose lease
// expired, releasing the task that recovery blocked. It never re-dispatches
// work: the outcome is the one a human or reconciliation job established.
func (s *Service) Resolve(ctx context.Context, input ResolveInput) (domain.WorkflowAttempt, bool, error) {
	if s.store == nil {
		return domain.WorkflowAttempt{}, false, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return domain.WorkflowAttempt{}, false, err
	}
	input.ErrorCode = strings.TrimSpace(input.ErrorCode)
	if input.Outcome != domain.WorkflowAttemptStateSucceeded && input.Outcome != domain.WorkflowAttemptStateFailed {
		return domain.WorkflowAttempt{}, false, apierr.Invalid("INVALID_ATTEMPT_RESULT", "Attempt result must be succeeded or failed", nil)
	}
	if len(input.ErrorCode) > 256 {
		return domain.WorkflowAttempt{}, false, apierr.Invalid("INVALID_ATTEMPT_ERROR_CODE", "Attempt error code is too long", nil)
	}
	fingerprint, err := requestFingerprint(struct {
		WorkflowID           domain.WorkflowID           `json:"workflowId"`
		AttemptID            domain.WorkflowAttemptID    `json:"attemptId"`
		Outcome              domain.WorkflowAttemptState `json:"outcome"`
		ErrorCode            string                      `json:"errorCode"`
		ExpectedTaskRevision int64                       `json:"expectedTaskRevision"`
	}{input.WorkflowID, input.AttemptID, input.Outcome, input.ErrorCode, input.ExpectedTaskRevision})
	if err != nil {
		return domain.WorkflowAttempt{}, false, apierr.InternalCause("WORKFLOW_FINGERPRINT_FAILED", "Failed to identify the resolve request", err)
	}
	rec, applied, err := s.store.ResolveUnknownWorkflowAttempt(
		ctx, input.WorkflowID, input.AttemptID, input.Outcome, input.ErrorCode,
		input.ExpectedTaskRevision, input.IdempotencyKey, fingerprint, s.clock().UTC(),
	)
	if err != nil {
		return domain.WorkflowAttempt{}, false, mapStoreError("WORKFLOW_ATTEMPT_RESOLVE_FAILED", "Failed to resolve workflow attempt", err)
	}
	return rec, applied, nil
}

// Retry returns a retry_wait task to READY after a revision check.
func (s *Service) Retry(ctx context.Context, input RetryInput) (domain.WorkflowTask, bool, error) {
	if s.store == nil {
		return domain.WorkflowTask{}, false, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return domain.WorkflowTask{}, false, err
	}
	if _, err := s.GetTask(ctx, input.WorkflowID, input.TaskID); err != nil {
		return domain.WorkflowTask{}, false, err
	}
	fingerprint, err := requestFingerprint(struct {
		WorkflowID       domain.WorkflowID     `json:"workflowId"`
		TaskID           domain.WorkflowTaskID `json:"taskId"`
		ExpectedRevision int64                 `json:"expectedRevision"`
	}{input.WorkflowID, input.TaskID, input.ExpectedRevision})
	if err != nil {
		return domain.WorkflowTask{}, false, apierr.InternalCause("WORKFLOW_FINGERPRINT_FAILED", "Failed to identify the retry request", err)
	}
	rec, applied, err := s.store.RetryWorkflowTask(
		ctx, input.WorkflowID, input.TaskID, input.ExpectedRevision, input.IdempotencyKey,
		fingerprint, s.clock().UTC(),
	)
	if err != nil {
		return domain.WorkflowTask{}, false, mapStoreError("WORKFLOW_TASK_RETRY_FAILED", "Failed to retry workflow task", err)
	}
	return rec, applied, nil
}

// RecoverExpired moves expired attempts to UNKNOWN and blocks their tasks.
func (s *Service) RecoverExpired(ctx context.Context) (int64, error) {
	if s.store == nil {
		return 0, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Workflow storage is unavailable")
	}
	count, err := s.store.RecoverExpiredWorkflowAttempts(ctx, s.clock().UTC())
	if err != nil {
		return 0, apierr.InternalCause("WORKFLOW_RECOVERY_FAILED", "Failed to recover expired workflow attempts", err)
	}
	return count, nil
}

func normalizeCreateInput(input CreateInput) CreateInput {
	input.Title = strings.TrimSpace(input.Title)
	input.Goal = strings.TrimSpace(input.Goal)
	for i := range input.Repositories {
		input.Repositories[i].RepoKey = domain.WorkflowRepoKey(strings.TrimSpace(string(input.Repositories[i].RepoKey)))
		input.Repositories[i].ProjectID = domain.ProjectID(strings.TrimSpace(string(input.Repositories[i].ProjectID)))
		input.Repositories[i].PathRef = strings.TrimSpace(input.Repositories[i].PathRef)
		input.Repositories[i].BaseSHA = strings.TrimSpace(input.Repositories[i].BaseSHA)
		input.Repositories[i].TargetRef = strings.TrimSpace(input.Repositories[i].TargetRef)
	}
	for i := range input.Tasks {
		input.Tasks[i].ID = domain.WorkflowTaskID(strings.TrimSpace(string(input.Tasks[i].ID)))
		input.Tasks[i].Title = strings.TrimSpace(input.Tasks[i].Title)
		input.Tasks[i].Brief = strings.TrimSpace(input.Tasks[i].Brief)
	}
	for i := range input.Dependencies {
		input.Dependencies[i].FromTaskID = domain.WorkflowTaskID(strings.TrimSpace(string(input.Dependencies[i].FromTaskID)))
		input.Dependencies[i].ToTaskID = domain.WorkflowTaskID(strings.TrimSpace(string(input.Dependencies[i].ToTaskID)))
	}
	for i := range input.TaskRepositories {
		binding := &input.TaskRepositories[i]
		binding.TaskID = domain.WorkflowTaskID(strings.TrimSpace(string(binding.TaskID)))
		binding.RepoKey = domain.WorkflowRepoKey(strings.TrimSpace(string(binding.RepoKey)))
		binding.BaseSHA = strings.TrimSpace(binding.BaseSHA)
		for j := range binding.AllowedPaths {
			binding.AllowedPaths[j] = strings.TrimSpace(binding.AllowedPaths[j])
		}
		if binding.AllowedPaths == nil {
			binding.AllowedPaths = []string{}
		}
		sort.Strings(binding.AllowedPaths)
	}
	sort.Slice(input.Repositories, func(i, j int) bool { return input.Repositories[i].RepoKey < input.Repositories[j].RepoKey })
	sort.Slice(input.Tasks, func(i, j int) bool { return input.Tasks[i].ID < input.Tasks[j].ID })
	sort.Slice(input.Dependencies, func(i, j int) bool {
		if input.Dependencies[i].FromTaskID != input.Dependencies[j].FromTaskID {
			return input.Dependencies[i].FromTaskID < input.Dependencies[j].FromTaskID
		}
		return input.Dependencies[i].ToTaskID < input.Dependencies[j].ToTaskID
	})
	sort.Slice(input.TaskRepositories, func(i, j int) bool {
		if input.TaskRepositories[i].TaskID != input.TaskRepositories[j].TaskID {
			return input.TaskRepositories[i].TaskID < input.TaskRepositories[j].TaskID
		}
		return input.TaskRepositories[i].RepoKey < input.TaskRepositories[j].RepoKey
	})
	return input
}

func normalizePage(limit, offset int64) (int64, int64, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return 0, 0, apierr.Invalid("INVALID_PAGINATION", "Limit must be between 1 and 100 and offset cannot be negative", nil)
	}
	return limit, offset, nil
}

func validateIdempotencyKey(key string) error {
	if strings.TrimSpace(key) == "" || len(key) > 200 || strings.TrimSpace(key) != key {
		return apierr.Invalid("INVALID_IDEMPOTENCY_KEY", "A non-empty idempotency key of at most 200 bytes is required", nil)
	}
	return nil
}

func requestFingerprint(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func randomLeaseToken() (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate lease token: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}

func mapStoreError(code, message string, err error) error {
	switch {
	case errors.Is(err, domain.ErrWorkflowProjectNotFound), errors.Is(err, domain.ErrWorkflowProjectArchived):
		return apierr.NotFound("WORKFLOW_PROJECT_NOT_FOUND", "A workflow repository references an unknown or archived project")
	case errors.Is(err, domain.ErrWorkflowIdempotencyConflict):
		return apierr.Conflict("WORKFLOW_IDEMPOTENCY_CONFLICT", "Idempotency key was already used for a different request", nil)
	case errors.Is(err, domain.ErrWorkflowRevisionConflict):
		return apierr.Conflict("WORKFLOW_REVISION_CONFLICT", "Workflow resource changed; reload it before retrying", nil)
	case errors.Is(err, domain.ErrWorkflowTaskNotClaimable):
		return apierr.Conflict("WORKFLOW_TASK_NOT_CLAIMABLE", "Workflow task is no longer ready to claim", nil)
	case errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict):
		return apierr.Conflict("WORKFLOW_ATTEMPT_LEASE_CONFLICT", "Workflow attempt lease is invalid or expired", nil)
	case errors.Is(err, domain.ErrInvalidWorkflowTransition):
		return apierr.Conflict("WORKFLOW_INVALID_TRANSITION", "Workflow state does not allow this transition", nil)
	case errors.Is(err, domain.ErrInvalidWorkflow),
		errors.Is(err, domain.ErrInvalidWorkflowState),
		errors.Is(err, domain.ErrInvalidWorkflowRepository),
		errors.Is(err, domain.ErrInvalidWorkflowTask),
		errors.Is(err, domain.ErrInvalidWorkflowTaskKind),
		errors.Is(err, domain.ErrInvalidWorkflowTaskState),
		errors.Is(err, domain.ErrInvalidWorkflowEdge),
		errors.Is(err, domain.ErrInvalidWorkflowTaskRepository),
		errors.Is(err, domain.ErrInvalidWorkflowAttempt),
		errors.Is(err, domain.ErrInvalidWorkflowAttemptState),
		errors.Is(err, domain.ErrInvalidWorkflowAttemptLease):
		return apierr.Invalid("INVALID_WORKFLOW_REQUEST", err.Error(), nil)
	default:
		return apierr.InternalCause(code, message, err)
	}
}
