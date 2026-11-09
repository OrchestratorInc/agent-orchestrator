package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	workflowsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// The workflow API is served by a service whose real implementation owns graph
// validation, idempotency, and lease bookkeeping. These tests drive the routes
// through a fake implementing controllers.WorkflowService so the HTTP contract
// — status codes, envelopes, pagination, and which field carries the lease
// capability — is pinned independently of that implementation. Each state
// transition the fake can refuse is configured explicitly rather than derived
// from a revision, because the real refusal logic lives in the service (and is
// covered by its own unit tests).

type fakeWorkflowService struct {
	workflow domain.Workflow
	items    []domain.Workflow
	tasks    []domain.WorkflowTask
	attempts []domain.WorkflowAttempt

	definition workflowsvc.Definition

	// applied controls the service's second return value: true means the write
	// inserted (201), false means the idempotency key replayed it (200).
	applied bool

	// resolveErr stands in for the resolved-attempt path's classified failures.
	resolveErr error

	// createErr, claimErr, and settleErr are returned verbatim, standing in for
	// the service's classified failures.
	createErr error
	claimErr  error
	settleErr error

	recovered int64

	lastKey   string
	lastInput workflowsvc.CreateInput
	lastPage  [3]int64
}

func (f *fakeWorkflowService) Create(_ context.Context, idempotencyKey string, input workflowsvc.CreateInput) (domain.Workflow, bool, error) {
	f.lastKey = idempotencyKey
	f.lastInput = input
	if f.createErr != nil {
		return domain.Workflow{}, false, f.createErr
	}
	return f.workflow, f.applied, nil
}

func (f *fakeWorkflowService) Get(context.Context, domain.WorkflowID) (domain.Workflow, error) {
	return f.workflow, nil
}

func (f *fakeWorkflowService) GetDefinition(context.Context, domain.WorkflowID) (workflowsvc.Definition, error) {
	return f.definition, nil
}

func (f *fakeWorkflowService) List(_ context.Context, state domain.WorkflowState, limit, offset int64) ([]domain.Workflow, error) {
	f.lastPage = [3]int64{int64(len(state)), limit, offset}
	return f.items, nil
}

func (f *fakeWorkflowService) ListTasks(_ context.Context, _ domain.WorkflowID, _ domain.WorkflowTaskState, limit, offset int64) ([]domain.WorkflowTask, error) {
	f.lastPage = [3]int64{0, limit, offset}
	return f.tasks, nil
}

func (f *fakeWorkflowService) GetTask(context.Context, domain.WorkflowID, domain.WorkflowTaskID) (domain.WorkflowTask, error) {
	return f.tasks[0], nil
}

func (f *fakeWorkflowService) ListAttempts(context.Context, domain.WorkflowID, domain.WorkflowTaskID) ([]domain.WorkflowAttempt, error) {
	return f.attempts, nil
}

func (f *fakeWorkflowService) Activate(context.Context, domain.WorkflowID, int64, string) (domain.Workflow, bool, error) {
	return f.workflow, true, nil
}

func (f *fakeWorkflowService) Claim(context.Context, workflowsvc.ClaimInput) (workflowsvc.ClaimResult, bool, error) {
	if f.claimErr != nil {
		return workflowsvc.ClaimResult{}, false, f.claimErr
	}
	return workflowsvc.ClaimResult{Attempt: f.attempts[0], LeaseToken: f.attempts[0].LeaseToken}, f.applied, nil
}

func (f *fakeWorkflowService) Start(context.Context, workflowsvc.StartInput) (domain.WorkflowAttempt, bool, error) {
	return f.attempts[0], true, nil
}

func (f *fakeWorkflowService) Settle(context.Context, workflowsvc.SettleInput) (domain.WorkflowAttempt, bool, error) {
	if f.settleErr != nil {
		return domain.WorkflowAttempt{}, false, f.settleErr
	}
	return f.attempts[0], true, nil
}

func (f *fakeWorkflowService) Retry(context.Context, workflowsvc.RetryInput) (domain.WorkflowTask, bool, error) {
	return f.tasks[0], true, nil
}

func (f *fakeWorkflowService) RecoverExpired(context.Context) (int64, error) {
	return f.recovered, nil
}

func (f *fakeWorkflowService) Resolve(
	context.Context,
	workflowsvc.ResolveInput,
) (domain.WorkflowAttempt, bool, error) {
	if f.resolveErr != nil {
		return domain.WorkflowAttempt{}, false, f.resolveErr
	}
	return f.attempts[0], true, nil
}

func workflowTestTime() time.Time { return time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC) }

// newWorkflowSample is the fixed workflow every workflow test reads back.
func newWorkflowSample() domain.Workflow {
	now := workflowTestTime()
	return domain.Workflow{
		ID: "workflow-1", Title: "Ship M1-04", Goal: "Expose the workflow core over HTTP",
		State: domain.WorkflowStateDraft, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
}

func newWorkflowTaskSample() domain.WorkflowTask {
	now := workflowTestTime()
	return domain.WorkflowTask{
		ID: "task-a", WorkflowID: "workflow-1", Kind: domain.WorkflowTaskKindDevelopment,
		Title: "Controller", State: domain.WorkflowTaskStateReady, MaxAttempts: 2,
		Revision: 2, CreatedAt: now, UpdatedAt: now,
	}
}

// newWorkflowAttemptSample carries a lease token, so any response that leaks it
// is caught by inspecting the JSON rather than trusting the projection.
func newWorkflowAttemptSample() domain.WorkflowAttempt {
	now := workflowTestTime()
	leaseUntil := now.Add(5 * time.Minute)
	return domain.WorkflowAttempt{
		ID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-a", AttemptNo: 1,
		WorkerID: "worker-1", LeaseToken: "lease-secret", LeaseUntil: &leaseUntil,
		State: domain.WorkflowAttemptStateClaimed, CreatedAt: now, UpdatedAt: now,
	}
}

// newWorkflowService builds a fake preloaded with one workflow, one task, one
// attempt, and the matching definition.
func newWorkflowService() *fakeWorkflowService {
	workflow := newWorkflowSample()
	task := newWorkflowTaskSample()
	attempt := newWorkflowAttemptSample()
	return &fakeWorkflowService{
		workflow: workflow,
		items:    []domain.Workflow{workflow},
		tasks:    []domain.WorkflowTask{task},
		attempts: []domain.WorkflowAttempt{attempt},
		definition: workflowsvc.Definition{
			Workflow: workflow,
			Repositories: []domain.WorkflowRepository{{
				WorkflowID: workflow.ID, RepoKey: "backend", ProjectID: "demo",
				PathRef: "backend", BaseSHA: "base-sha", TargetRef: "main",
			}},
			Tasks: []domain.WorkflowTask{task},
			Edges: []domain.WorkflowEdge{{
				WorkflowID: workflow.ID, FromTaskID: "task-a", ToTaskID: "task-b",
			}},
			TaskRepositories: []domain.WorkflowTaskRepository{{
				WorkflowID: workflow.ID, TaskID: "task-a", RepoKey: "backend",
				AllowedPaths: []string{"backend/internal"}, BaseSHA: "task-base",
			}},
		},
		recovered: 3,
		applied:   true,
	}
}

// newWorkflowTestServer serves the workflow routes over the full daemon router,
// so the tests also prove the routes are mounted inside the bounded /api/v1
// group with the same middleware as every other controller.
func newWorkflowTestServer(t *testing.T, service controllers.WorkflowService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var deps httpd.APIDeps
	if service != nil {
		deps.Workflows = service
	}
	deps.WorkflowExecutionPermits, _ = service.(controllers.ExecutionPermitReader)
	deps.WorkflowRetrySchedule = func(context.Context, domain.WorkflowID, domain.WorkflowTaskID, int64) error { return nil }
	deps.WorkflowExecutionFence = func(context.Context) (domain.SchedulerFence, error) {
		return domain.SchedulerFence{OwnerID: "test", Epoch: 1}, nil
	}
	deps.WorkflowRecovery, _ = service.(controllers.WorkflowRecovery)
	deps = approvedWorkflowDeps(deps)
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// newRealWorkflowService wires the production service over a real temp sqlite
// store, proving the controller talks to the implemented contract rather than
// only to the fake.
func newRealWorkflowService(t *testing.T) (controllers.WorkflowService, *httptest.Server) {
	t.Helper()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "demo", Path: t.TempDir(), RegisteredAt: workflowTestTime(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	clock := func() time.Time { return time.Now().UTC() }
	service := workflowsvc.New(workflowsvc.Deps{
		Store:         store,
		Clock:         clock,
		Admission:     sqlitetest.NewAdmissionFixture(t, store, clock),
		GraphLimits:   domain.WorkflowGraphLimits{MaxTasks: 50, MaxDepth: 20, MaxFanOut: 20},
		LeaseDuration: 5 * time.Minute,
	})
	deps := approvedWorkflowDeps(httpd.APIDeps{Workflows: service, WorkflowExecutionPermits: store, WorkflowRecovery: store})
	deps.WorkflowRetrySchedule = func(context.Context, domain.WorkflowID, domain.WorkflowTaskID, int64) error { return nil }
	deps.WorkflowExecutionFence = func(ctx context.Context) (domain.SchedulerFence, error) {
		leader, err := store.GetSchedulerLeader(ctx)
		return leader.Fence, err
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return service, srv
}

const workflowCreateBody = `{
  "title": "Ship M1-04",
  "goal": "Expose the workflow core over HTTP",
  "idempotencyKey": "create-key-1",
  "repositories": [{"repoKey": "backend", "projectId": "demo", "pathRef": "backend"}],
  "tasks": [
    {"id": "task-a", "kind": "development", "title": "Controller", "maxAttempts": 2},
    {"id": "task-b", "kind": "testing", "title": "Routes"}
  ],
  "dependencies": [{"fromTaskId": "task-a", "toTaskId": "task-b"}],
  "taskRepositories": [{"taskId": "task-a", "repoKey": "backend", "allowedPaths": ["backend/internal"]}]
}`

// TestWorkflowsCreateReplayAndConflict pins the two idempotency outcomes the
// route must distinguish: a fresh insert answers 201, the same key answers 200
// with the SAME workflow, and the same key with a different body is a 409 the
// service classifies as WORKFLOW_IDEMPOTENCY_CONFLICT.
func TestWorkflowsCreateReplayAndConflict(t *testing.T) {
	service := newWorkflowService()
	srv := newWorkflowTestServer(t, service)

	body, status, headers := doWorkflowRequest(t, srv, "POST", "/api/v1/workflows", workflowCreateBody)
	assertJSON(t, headers)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", status, body)
	}
	var created controllers.WorkflowEnvelope
	mustJSON(t, body, &created)
	if created.Workflow.ID != "workflow-1" || created.Workflow.State != "draft" || created.Workflow.Revision != 1 {
		t.Fatalf("created workflow = %#v", created.Workflow)
	}
	// The body's idempotencyKey is what reaches the service: no header is read.
	if service.lastKey != "create-key-1" {
		t.Fatalf("service saw idempotency key %q, want create-key-1", service.lastKey)
	}
	// The nested graph must survive the DTO→service mapping intact.
	if len(service.lastInput.Repositories) != 1 ||
		service.lastInput.Repositories[0].PathRef != "backend" ||
		len(service.lastInput.Tasks) != 2 ||
		service.lastInput.Tasks[0].MaxAttempts != 2 ||
		len(service.lastInput.Dependencies) != 1 ||
		service.lastInput.Dependencies[0].FromTaskID != "task-a" ||
		len(service.lastInput.TaskRepositories) != 1 ||
		len(service.lastInput.TaskRepositories[0].AllowedPaths) != 1 {
		t.Fatalf("create input lost graph detail: %#v", service.lastInput)
	}

	// A replay (applied=false) answers 200 with the identical workflow.
	service.applied = false
	body, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows", workflowCreateBody)
	if status != http.StatusOK {
		t.Fatalf("replay status = %d, want 200; body=%s", status, body)
	}
	var replayed controllers.WorkflowEnvelope
	mustJSON(t, body, &replayed)
	if replayed.Workflow.ID != created.Workflow.ID {
		t.Fatalf("replay returned %q, want the original %q", replayed.Workflow.ID, created.Workflow.ID)
	}

	// The same key with a different body is a conflict, not a second create.
	service.createErr = apierr.Conflict("WORKFLOW_IDEMPOTENCY_CONFLICT",
		"Idempotency key was already used for a different request", nil)
	body, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows",
		strings.Replace(workflowCreateBody, `"Ship M1-04"`, `"Different title"`, 1))
	assertErrorCode(t, body, status, http.StatusConflict, "WORKFLOW_IDEMPOTENCY_CONFLICT")
}

// TestWorkflowsCreateRejectsCyclicGraph pins that a graph the service rejects
// surfaces as a plain 400 with the service's code, not a 500.
func TestWorkflowsCreateRejectsCyclicGraph(t *testing.T) {
	service := newWorkflowService()
	service.createErr = apierr.Invalid("INVALID_WORKFLOW_GRAPH", "workflow graph: dependency cycle", nil)
	srv := newWorkflowTestServer(t, service)

	body, status, _ := doWorkflowRequest(t, srv, "POST", "/api/v1/workflows", workflowCreateBody)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_WORKFLOW_GRAPH")
}

// TestWorkflowsCreateRejectsOversizedBody pins the 1 MiB create cap: a workflow
// graph is large, but not unbounded, and the reader must refuse the excess
// rather than buffer it.
func TestWorkflowsCreateRejectsOversizedBody(t *testing.T) {
	srv := newWorkflowTestServer(t, newWorkflowService())
	oversized := `{"title":"` + strings.Repeat("x", (1<<20)+1) + `"}`
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/workflows", oversized)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", status, body)
	}
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
}

// TestWorkflowsListPagination pins the page contract: the created workflow comes
// back, a bad state is INVALID_QUERY, an out-of-range limit is the service's
// INVALID_PAGINATION, limit=0 means the 50 default, and a FULL page reports a
// nextCursor while a short one does not.
func TestWorkflowsListPagination(t *testing.T) {
	service := newWorkflowService()
	srv := newWorkflowTestServer(t, service)

	body, status, _ := doWorkflowRequest(t, srv, "GET", "/api/v1/workflows", "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", status, body)
	}
	var page controllers.ListWorkflowsResponse
	mustJSON(t, body, &page)
	if len(page.Workflows) != 1 || page.Workflows[0].ID != "workflow-1" {
		t.Fatalf("list = %#v", page)
	}
	// A page shorter than the limit proves nothing follows, so no cursor.
	if page.NextCursor != "" {
		t.Fatalf("nextCursor = %q, want empty for a short page", page.NextCursor)
	}

	body, status, _ = doWorkflowRequest(t, srv, "GET", "/api/v1/workflows?state=nonsense", "")
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_QUERY")

	body, status, _ = doWorkflowRequest(t, srv, "GET", "/api/v1/workflows?limit=101", "")
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_PAGINATION")

	// limit=0 is the service's "default" spelling; the route must not reject it.
	_, status, _ = doWorkflowRequest(t, srv, "GET", "/api/v1/workflows?limit=0", "")
	if status != http.StatusOK {
		t.Fatalf("limit=0 status = %d, want 200", status)
	}
	if service.lastPage[1] != 50 {
		t.Fatalf("limit=0 reached the service as %d, want the 50 default", service.lastPage[1])
	}

	// A full page may have more, so it reports the next offset as a cursor.
	service.items = []domain.Workflow{newWorkflowSample()}
	_, status, _ = doWorkflowRequest(t, srv, "GET", "/api/v1/workflows?limit=1", "")
	if status != http.StatusOK {
		t.Fatalf("full page status = %d", status)
	}
	body, _, _ = doWorkflowRequest(t, srv, "GET", "/api/v1/workflows?limit=1", "")
	mustJSON(t, body, &page)
	if page.NextCursor != "MQ" {
		t.Fatalf("nextCursor = %q, want %q (base64 of 1)", page.NextCursor, "MQ")
	}
}

// TestWorkflowsGetUnknownWorkflowIs404 pins the missing-resource mapping. Only
// the real service can answer "missing", so this runs against it.
func TestWorkflowsGetUnknownWorkflowIs404(t *testing.T) {
	_, srv := newRealWorkflowService(t)
	body, status, _ := doWorkflowRequest(t, srv, "GET", "/api/v1/workflows/workflow-missing", "")
	assertErrorCode(t, body, status, http.StatusNotFound, "WORKFLOW_NOT_FOUND")
}

// TestWorkflowsDefinitionIncludesGraph pins that the detail route returns the
// repositories, tasks, edges, and task scopes — not just the workflow row.
func TestWorkflowsDefinitionIncludesGraph(t *testing.T) {
	srv := newWorkflowTestServer(t, newWorkflowService())

	body, status, _ := doWorkflowRequest(t, srv, "GET", "/api/v1/workflows/workflow-1", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d; body=%s", status, body)
	}
	var definition controllers.WorkflowDefinitionResponse
	mustJSON(t, body, &definition)
	if definition.Workflow.ID != "workflow-1" {
		t.Fatalf("workflow = %#v", definition.Workflow)
	}
	if len(definition.Repositories) != 1 || definition.Repositories[0].RepoKey != "backend" ||
		definition.Repositories[0].PathRef != "backend" {
		t.Fatalf("repositories = %#v", definition.Repositories)
	}
	if len(definition.Tasks) != 1 || definition.Tasks[0].ID != "task-a" {
		t.Fatalf("tasks = %#v", definition.Tasks)
	}
	if len(definition.Edges) != 1 ||
		definition.Edges[0].FromTaskID != "task-a" || definition.Edges[0].ToTaskID != "task-b" {
		t.Fatalf("edges = %#v", definition.Edges)
	}
	if len(definition.TaskRepositories) != 1 ||
		len(definition.TaskRepositories[0].AllowedPaths) != 1 ||
		definition.TaskRepositories[0].AllowedPaths[0] != "backend/internal" {
		t.Fatalf("task repositories = %#v", definition.TaskRepositories)
	}
}

// TestWorkflowsActivateRevisionConflict pins the optimistic-concurrency
// mapping: a stale expectedRevision is a 409, and the accepted revision answers
// 200 with the activated state.
func TestWorkflowsActivateRevisionConflict(t *testing.T) {
	active := newWorkflowSample()
	active.State = domain.WorkflowStateActive
	active.Revision = 2

	service := newWorkflowService()
	service.workflow = active
	srv := newWorkflowTestServer(t, service)

	// The real service owns the revision check; drive it there.
	_, realSrv := newRealWorkflowService(t)
	body, status, _ := doWorkflowRequest(t, realSrv, "POST", "/api/v1/workflows/workflow-missing/activate",
		`{"expectedRevision":1,"idempotencyKey":"activate-key"}`)
	assertErrorCode(t, body, status, http.StatusNotFound, "WORKFLOW_NOT_FOUND")

	body, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows/workflow-1/activate",
		`{"expectedRevision":1,"idempotencyKey":"activate-key"}`)
	if status != http.StatusOK {
		t.Fatalf("activate status = %d; body=%s", status, body)
	}
	var activated controllers.WorkflowEnvelope
	mustJSON(t, body, &activated)
	if activated.Workflow.State != "active" || activated.Workflow.Revision != 2 {
		t.Fatalf("activated workflow = %#v", activated.Workflow)
	}
}

// TestWorkflowsActivateStaleRevisionIs409 drives the real service through the
// full graph lifecycle so the conflict code comes from the implementation, not
// from the fake.
func TestWorkflowsActivateStaleRevisionIs409(t *testing.T) {
	_, srv := newRealWorkflowService(t)
	body, status, _ := doWorkflowRequest(t, srv, "POST", "/api/v1/workflows", workflowCreateBody)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", status, body)
	}
	var created controllers.WorkflowEnvelope
	mustJSON(t, body, &created)
	path := "/api/v1/workflows/" + created.Workflow.ID + "/activate"

	body, status, _ = doWorkflowRequest(t, srv, "POST", path, `{"expectedRevision":9,"idempotencyKey":"stale-key"}`)
	assertErrorCode(t, body, status, http.StatusConflict, "WORKFLOW_REVISION_CONFLICT")

	body, status, _ = doWorkflowRequest(t, srv, "POST", path, `{"expectedRevision":1,"idempotencyKey":"fresh-key"}`)
	if status != http.StatusOK {
		t.Fatalf("activate status = %d; body=%s", status, body)
	}
	var activated controllers.WorkflowEnvelope
	mustJSON(t, body, &activated)
	if activated.Workflow.State != "active" || activated.Workflow.Revision != 2 {
		t.Fatalf("activated workflow = %#v", activated.Workflow)
	}
}

// TestWorkflowsTaskDetailCarriesAttemptsWithoutLeaseToken pins the capability
// boundary: the task detail lists its attempts, no attempt object carries a
// leaseToken field, and only the claim response exposes the token — top level.
func TestWorkflowsTaskDetailCarriesAttemptsWithoutLeaseToken(t *testing.T) {
	service := newWorkflowService()
	srv := newWorkflowTestServer(t, service)

	body, status, _ := doWorkflowRequest(t, srv, "GET", "/api/v1/workflows/workflow-1/tasks/task-a", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d; body=%s", status, body)
	}
	var detail controllers.WorkflowTaskEnvelope
	mustJSON(t, body, &detail)
	if detail.Task.ID != "task-a" {
		t.Fatalf("task = %#v", detail.Task)
	}
	if len(detail.Attempts) != 1 || detail.Attempts[0].ID != "attempt-1" {
		t.Fatalf("attempts = %#v", detail.Attempts)
	}

	// The raw JSON must contain no leaseToken anywhere in an attempt object.
	var raw struct {
		Task     map[string]any   `json:"task"`
		Attempts []map[string]any `json:"attempts"`
	}
	mustJSON(t, body, &raw)
	for _, attempt := range raw.Attempts {
		if _, leaked := attempt["leaseToken"]; leaked {
			t.Fatalf("attempt object leaked leaseToken: %v", attempt)
		}
	}
	if strings.Contains(string(body), "lease-secret") {
		t.Fatalf("response leaked the lease capability: %s", body)
	}

	// Claim 同样不向调用方返回内部租约。
	body, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows/workflow-1/tasks/task-a/claim",
		`{"expectedRevision":2,"workerId":"worker-1","idempotencyKey":"claim-key-1"}`)
	if status != http.StatusCreated {
		t.Fatalf("claim status = %d; body=%s", status, body)
	}
	var claim struct {
		Attempt    map[string]any `json:"attempt"`
		LeaseToken string         `json:"leaseToken"`
	}
	mustJSON(t, body, &claim)
	if claim.LeaseToken != "" || strings.Contains(string(body), "lease-secret") {
		t.Fatalf("claim leaked its internal lease: %s", body)
	}
	if _, leaked := claim.Attempt["leaseToken"]; leaked {
		t.Fatalf("claim attempt object also carries leaseToken: %v", claim.Attempt)
	}

	// A replayed claim answers 200 with the same body shape.
	service.applied = false
	_, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows/workflow-1/tasks/task-a/claim",
		`{"expectedRevision":2,"workerId":"worker-1","idempotencyKey":"claim-key-1"}`)
	if status != http.StatusOK {
		t.Fatalf("replayed claim status = %d, want 200", status)
	}
}

// TestWorkflowsClaimTwiceIsConflict pins the second-claim mapping: the service
// refuses a task that is no longer claimable with a 409, and the route passes
// that code through unchanged.
func TestWorkflowsClaimTwiceIsConflict(t *testing.T) {
	service := newWorkflowService()
	srv := newWorkflowTestServer(t, service)

	body, status, _ := doWorkflowRequest(t, srv, "POST", "/api/v1/workflows/workflow-1/tasks/task-a/claim",
		`{"expectedRevision":2,"workerId":"worker-1","idempotencyKey":"claim-key-1"}`)
	if status != http.StatusCreated {
		t.Fatalf("first claim status = %d; body=%s", status, body)
	}

	service.claimErr = apierr.Conflict("WORKFLOW_TASK_NOT_CLAIMABLE",
		"Workflow task is no longer ready to claim", nil)
	body, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows/workflow-1/tasks/task-a/claim",
		`{"expectedRevision":2,"workerId":"worker-2","idempotencyKey":"claim-key-2"}`)
	assertErrorCode(t, body, status, http.StatusConflict, "WORKFLOW_TASK_NOT_CLAIMABLE")
}

// TestWorkflowsSettleWrongLeaseIsConflict pins the lease-capability refusal:
// a settle carrying the wrong token is a 409 the service classifies as
// WORKFLOW_ATTEMPT_LEASE_CONFLICT, and the accepted settle answers 200.
func TestWorkflowsSettleWrongLeaseIsConflict(t *testing.T) {
	service := newWorkflowService()
	srv := newWorkflowTestServer(t, service)

	service.settleErr = apierr.Conflict("WORKFLOW_ATTEMPT_LEASE_CONFLICT",
		"Workflow attempt lease is invalid or expired", nil)
	body, status, _ := doWorkflowRequest(t, srv, "POST", "/api/v1/workflows/workflow-1/attempts/attempt-1/settle",
		`{"result":"succeeded","idempotencyKey":"settle-key-1"}`)
	assertErrorCode(t, body, status, http.StatusConflict, "WORKFLOW_ATTEMPT_LEASE_CONFLICT")

	service.settleErr = nil
	body, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows/workflow-1/attempts/attempt-1/settle",
		`{"result":"succeeded","idempotencyKey":"settle-key-2"}`)
	if status != http.StatusOK {
		t.Fatalf("settle status = %d; body=%s", status, body)
	}
	var settled controllers.WorkflowAttemptEnvelope
	mustJSON(t, body, &settled)
	if settled.Attempt.ID != "attempt-1" {
		t.Fatalf("settled attempt = %#v", settled.Attempt)
	}
	if strings.Contains(string(body), "lease-secret") {
		t.Fatalf("settle response echoed the lease capability: %s", body)
	}
}

// TestWorkflowsRetryReturnsTaskWithAttempts pins the retry route: it answers
// 200 with the reopened task and its attempt history, the same envelope a task
// read returns, so a worker sees the full state after a retry.
func TestWorkflowsRetryReturnsTaskWithAttempts(t *testing.T) {
	srv := newWorkflowTestServer(t, newWorkflowService())
	body, status, _ := doWorkflowRequest(t, srv, "POST", "/api/v1/workflows/workflow-1/tasks/task-a/retry",
		`{"expectedRevision":2,"idempotencyKey":"retry-key-1"}`)
	if status != http.StatusOK {
		t.Fatalf("retry status = %d; body=%s", status, body)
	}
	var envelope controllers.WorkflowTaskEnvelope
	mustJSON(t, body, &envelope)
	if envelope.Task.ID != "task-a" {
		t.Fatalf("retried task = %#v", envelope.Task)
	}
	if len(envelope.Attempts) != 1 || envelope.Attempts[0].ID != "attempt-1" {
		t.Fatalf("attempts = %#v", envelope.Attempts)
	}
}

// TestWorkflowsRecoverExpiredReportsCount pins the body-less sweep route.
func TestWorkflowsRecoverExpiredReportsCount(t *testing.T) {
	srv := newWorkflowTestServer(t, newWorkflowService())
	body, status, _ := doWorkflowRequest(t, srv, "POST", "/api/v1/workflows/recover-expired", `{"idempotencyKey":"recovery-test"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d; body=%s", status, body)
	}
	var response controllers.RecoverExpiredWorkflowsResponse
	mustJSON(t, body, &response)
	if response.Recovered != 3 {
		t.Fatalf("recovered = %d, want 3", response.Recovered)
	}
}

// TestWorkflowsRoutesAreStubsWithoutService pins the nil-service contract: every
// workflow route stays mounted and answers 501 with the operation's own spec
// slice. The exact method+path strings below must match the spec operations, or
// apispec.NotImplemented panics — which is itself the regression signal.
func TestWorkflowsRoutesAreStubsWithoutService(t *testing.T) {
	srv := newWorkflowTestServer(t, nil)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/workflows"},
		{http.MethodPost, "/api/v1/workflows"},
		{http.MethodPost, "/api/v1/workflows/recover-expired"},
		{http.MethodGet, "/api/v1/workflows/workflow-1"},
		{http.MethodGet, "/api/v1/workflows/workflow-1/tasks"},
		{http.MethodGet, "/api/v1/workflows/workflow-1/tasks/task-a"},
		{http.MethodPost, "/api/v1/workflows/workflow-1/activate"},
		{http.MethodPost, "/api/v1/workflows/workflow-1/tasks/task-a/claim"},
		{http.MethodPost, "/api/v1/workflows/workflow-1/attempts/attempt-1/start"},
		{http.MethodPost, "/api/v1/workflows/workflow-1/attempts/attempt-1/settle"},
		{http.MethodPost, "/api/v1/workflows/workflow-1/tasks/task-a/retry"},
	} {
		body, status, headers := doWorkflowRequest(t, srv, route.method, route.path, "")
		assertJSON(t, headers)
		if status != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d, want 501; body=%s", route.method, route.path, status, body)
		}
		var notImplemented struct {
			Code string         `json:"code"`
			Spec map[string]any `json:"spec"`
		}
		mustJSON(t, body, &notImplemented)
		if notImplemented.Code != "NOT_IMPLEMENTED" {
			t.Fatalf("%s %s code = %q", route.method, route.path, notImplemented.Code)
		}
		if notImplemented.Spec["operationId"] == nil {
			t.Fatalf("%s %s 501 body carried no spec slice", route.method, route.path)
		}
	}
}

// TestWorkflowsEndToEndOverRealService drives create → definition → list →
// activate → claim → start → settle through the production service and a real
// temp sqlite database, proving the controller's mapping matches what the
// implementation actually returns.
func TestWorkflowsEndToEndOverRealService(t *testing.T) {
	_, srv := newRealWorkflowService(t)

	body, status, _ := doWorkflowRequest(t, srv, "POST", "/api/v1/workflows", workflowCreateBody)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", status, body)
	}
	var created controllers.WorkflowEnvelope
	mustJSON(t, body, &created)
	workflowID := created.Workflow.ID
	if workflowID == "" || created.Workflow.State != "draft" || created.Workflow.Revision != 1 {
		t.Fatalf("created workflow = %#v", created.Workflow)
	}

	// The same key replays with 200 and the same id.
	body, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows", workflowCreateBody)
	if status != http.StatusOK {
		t.Fatalf("replay status = %d; body=%s", status, body)
	}
	var replayed controllers.WorkflowEnvelope
	mustJSON(t, body, &replayed)
	if replayed.Workflow.ID != workflowID {
		t.Fatalf("replay id = %q, want %q", replayed.Workflow.ID, workflowID)
	}

	// A different body under the same key is the idempotency conflict.
	body, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows",
		strings.Replace(workflowCreateBody, `"Ship M1-04"`, `"Different"`, 1))
	assertErrorCode(t, body, status, http.StatusConflict, "WORKFLOW_IDEMPOTENCY_CONFLICT")

	// A cyclic graph is rejected before anything is stored.
	body, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows", `{
	  "title": "Cyclic", "goal": "Prove the cycle is rejected", "idempotencyKey": "cycle-key",
	  "repositories": [{"repoKey": "backend", "projectId": "demo"}],
	  "tasks": [{"id": "task-a", "kind": "development", "title": "A"},
	            {"id": "task-b", "kind": "testing", "title": "B"}],
	  "dependencies": [{"fromTaskId": "task-a", "toTaskId": "task-b"},
	                   {"fromTaskId": "task-b", "toTaskId": "task-a"}]
	}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_WORKFLOW_GRAPH")

	// The definition detail carries the whole persisted graph.
	body, status, _ = doWorkflowRequest(t, srv, "GET", "/api/v1/workflows/"+workflowID, "")
	if status != http.StatusOK {
		t.Fatalf("definition status = %d; body=%s", status, body)
	}
	var definition controllers.WorkflowDefinitionResponse
	mustJSON(t, body, &definition)
	if len(definition.Repositories) != 1 || definition.Repositories[0].RepoKey != "backend" {
		t.Fatalf("repositories = %#v", definition.Repositories)
	}
	if len(definition.Tasks) != 2 {
		t.Fatalf("tasks = %#v", definition.Tasks)
	}
	if len(definition.Edges) != 1 {
		t.Fatalf("edges = %#v", definition.Edges)
	}
	if len(definition.TaskRepositories) != 1 ||
		len(definition.TaskRepositories[0].AllowedPaths) != 1 {
		t.Fatalf("task repositories = %#v", definition.TaskRepositories)
	}

	// The list carries it too.
	body, status, _ = doWorkflowRequest(t, srv, "GET", "/api/v1/workflows", "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", status, body)
	}
	var page controllers.ListWorkflowsResponse
	mustJSON(t, body, &page)
	if len(page.Workflows) != 1 || page.Workflows[0].ID != workflowID {
		t.Fatalf("list = %#v", page)
	}

	// A bad task state is the query's problem, not the service's.
	body, status, _ = doWorkflowRequest(t, srv, "GET", "/api/v1/workflows/"+workflowID+"/tasks?state=bogus", "")
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_QUERY")

	// Activation promotes the root task to ready.
	body, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows/"+workflowID+"/activate",
		`{"expectedRevision":1,"idempotencyKey":"activate-key"}`)
	if status != http.StatusOK {
		t.Fatalf("activate status = %d; body=%s", status, body)
	}
	var activated controllers.WorkflowEnvelope
	mustJSON(t, body, &activated)
	if activated.Workflow.State != "active" || activated.Workflow.Revision != 2 {
		t.Fatalf("activated = %#v", activated.Workflow)
	}

	body, status, _ = doWorkflowRequest(t, srv, "GET", "/api/v1/workflows/"+workflowID+"/tasks", "")
	if status != http.StatusOK {
		t.Fatalf("tasks status = %d; body=%s", status, body)
	}
	var tasks controllers.ListWorkflowTasksResponse
	mustJSON(t, body, &tasks)
	if len(tasks.Tasks) != 2 {
		t.Fatalf("tasks = %#v", tasks.Tasks)
	}
	var ready *controllers.WorkflowTaskResponse
	for i := range tasks.Tasks {
		if tasks.Tasks[i].State == "ready" {
			ready = &tasks.Tasks[i]
		}
	}
	if ready == nil {
		t.Fatalf("no task became ready after activation: %#v", tasks.Tasks)
	}

	// Claiming issues the capability, and replaying the claim answers 200.
	claimPath := "/api/v1/workflows/" + workflowID + "/tasks/" + ready.ID + "/claim"
	claimBody := `{"expectedRevision":` + itoa(ready.Revision) + `,"workerId":"worker-1","idempotencyKey":"claim-key"}`
	body, status, _ = doWorkflowRequest(t, srv, "POST", claimPath, claimBody)
	if status != http.StatusCreated {
		t.Fatalf("claim status = %d; body=%s", status, body)
	}
	var claim controllers.ClaimWorkflowTaskResponse
	mustJSON(t, body, &claim)
	if strings.Contains(string(body), "leaseToken") {
		t.Fatal("claim response leaked its internal lease")
	}
	if claim.Attempt.State != "claimed" || claim.Attempt.AttemptNo != 1 {
		t.Fatalf("claimed attempt = %#v", claim.Attempt)
	}

	body, status, _ = doWorkflowRequest(t, srv, "POST", claimPath, claimBody)
	if status != http.StatusOK {
		t.Fatalf("replayed claim status = %d; body=%s", status, body)
	}
	var replayClaim controllers.ClaimWorkflowTaskResponse
	mustJSON(t, body, &replayClaim)
	if replayClaim.Attempt.ID != claim.Attempt.ID {
		t.Fatalf("replayed claim = %#v, want the original %#v", replayClaim, claim)
	}

	// A second worker cannot claim the same task.
	body, status, _ = doWorkflowRequest(t, srv, "POST", claimPath,
		`{"expectedRevision":`+itoa(ready.Revision)+`,"workerId":"worker-2","idempotencyKey":"claim-key-2"}`)
	assertErrorCode(t, body, status, http.StatusConflict, "WORKFLOW_TASK_NOT_CLAIMABLE")

	// The task detail shows the attempt history and never the token.
	body, status, _ = doWorkflowRequest(t, srv, "GET", "/api/v1/workflows/"+workflowID+"/tasks/"+ready.ID, "")
	if status != http.StatusOK {
		t.Fatalf("task status = %d; body=%s", status, body)
	}
	var detail controllers.WorkflowTaskEnvelope
	mustJSON(t, body, &detail)
	if len(detail.Attempts) != 1 || detail.Attempts[0].ID != claim.Attempt.ID {
		t.Fatalf("attempts = %#v", detail.Attempts)
	}
	if strings.Contains(string(body), "leaseToken") {
		t.Fatalf("task detail leaked the lease capability: %s", body)
	}

	// Start, then a wrong token is a lease conflict and the right one settles.
	startPath := "/api/v1/workflows/" + workflowID + "/attempts/" + claim.Attempt.ID + "/start"
	body, status, _ = doWorkflowRequest(t, srv, "POST", startPath,
		`{"idempotencyKey":"start-key"}`)
	if status != http.StatusOK {
		t.Fatalf("start status = %d; body=%s", status, body)
	}
	var started controllers.WorkflowAttemptEnvelope
	mustJSON(t, body, &started)
	if started.Attempt.State != "running" {
		t.Fatalf("started attempt = %#v", started.Attempt)
	}

	settlePath := "/api/v1/workflows/" + workflowID + "/attempts/" + claim.Attempt.ID + "/settle"
	body, status, _ = doWorkflowRequest(t, srv, "POST", settlePath,
		`{"leaseToken":"wrong-token","result":"succeeded","idempotencyKey":"settle-bad"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")

	body, status, _ = doWorkflowRequest(t, srv, "POST", settlePath,
		`{"result":"succeeded","idempotencyKey":"settle-key"}`)
	if status != http.StatusOK {
		t.Fatalf("settle status = %d; body=%s", status, body)
	}
	var settled controllers.WorkflowAttemptEnvelope
	mustJSON(t, body, &settled)
	if settled.Attempt.State != "succeeded" {
		t.Fatalf("settled attempt = %#v", settled.Attempt)
	}
	if strings.Contains(string(body), "leaseToken") {
		t.Fatalf("settle response leaked the lease capability: %s", body)
	}

	// The sweep is callable and reports a count, including on an idle workflow.
	body, status, _ = doWorkflowRequest(t, srv, "POST", "/api/v1/workflows/recover-expired", `{"idempotencyKey":"recovery-e2e"}`)
	if status != http.StatusOK {
		t.Fatalf("recover status = %d; body=%s", status, body)
	}
	var recovered controllers.RecoverExpiredWorkflowsResponse
	mustJSON(t, body, &recovered)
	if recovered.Recovered < 0 {
		t.Fatalf("recovered = %d", recovered.Recovered)
	}
}

// itoa renders a revision for a JSON body the tests build by hand.
func itoa(value int64) string { return strconv.FormatInt(value, 10) }
