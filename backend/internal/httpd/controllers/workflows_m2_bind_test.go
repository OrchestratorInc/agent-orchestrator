package controllers_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	executionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// These tests drive the M2 bind/recovery/observe routes through a real chi router over
// httptest. The acceptance explicitly rejected service-layer-only evidence: the B-03 defect was
// a handler that passed ProjectID:"" to a service that was individually correct, so the proof
// has to be at the wire.

// bindRecorder is a scripted AttemptBinder that records what the handler actually sent.
type bindRecorder struct {
	mu sync.Mutex
	// calls counts BindAttempt invocations, which is how the concurrency test proves exactly
	// one spawn was attempted.
	calls int
	// requests captures every SpawnRequest, in call order.
	requests         []ports.SpawnRequest
	repos            [][]domain.WorkflowAttemptRepoBinding
	outcome          executionsvc.BindOutcome
	bindErr          error
	observing        ports.SessionObservation
	state            domain.WorkflowBindingState
	observeErr       error
	reconcileOutcome executionsvc.BindOutcome
	reconcileErr     error
	resumeOutcome    executionsvc.BindOutcome
	resumeErr        error
	live             bool
	liveReason       string
}

func (b *bindRecorder) BindAttempt(
	_ context.Context,
	request ports.SpawnRequest,
	repositories []domain.WorkflowAttemptRepoBinding,
) (executionsvc.BindOutcome, error) {
	b.mu.Lock()
	b.calls++
	b.requests = append(b.requests, request)
	b.repos = append(b.repos, repositories)
	b.mu.Unlock()
	if b.bindErr != nil {
		return executionsvc.BindOutcome{}, b.bindErr
	}
	return b.outcome, nil
}

func (b *bindRecorder) ObserveSession(
	context.Context,
	domain.WorkflowAttemptID,
) (ports.SessionObservation, domain.WorkflowBindingState, error) {
	return b.observing, b.state, b.observeErr
}

func (b *bindRecorder) Reconcile(
	context.Context,
	domain.WorkflowAttemptID,
) (executionsvc.BindOutcome, error) {
	return b.reconcileOutcome, b.reconcileErr
}

// Resume and LiveTarget are the OPTIONAL capabilities: implementing them here is what the
// wrapper type below opts into.
func (b *bindRecorder) Resume(
	context.Context,
	domain.WorkflowAttemptID,
) (executionsvc.BindOutcome, error) {
	return b.resumeOutcome, b.resumeErr
}

func (b *bindRecorder) LiveTarget(
	context.Context,
	domain.WorkflowAttemptID,
) (domain.WorkflowAttemptBinding, ports.SessionObservation, bool, string, error) {
	return domain.WorkflowAttemptBinding{}, b.observing, b.live, b.liveReason, nil
}

func (b *bindRecorder) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func (b *bindRecorder) capturedRequests() []ports.SpawnRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]ports.SpawnRequest(nil), b.requests...)
}

// bindOnly exposes only the WIRED AttemptBinder methods, so a test can prove the 501 path is
// reachable. It deliberately does not embed *bindRecorder: embedding would promote Resume and
// LiveTarget, and the type assertion is exactly what the test is trying to fail.
type bindOnly struct {
	inner *bindRecorder
}

func (b bindOnly) BindAttempt(
	ctx context.Context,
	request ports.SpawnRequest,
	repositories []domain.WorkflowAttemptRepoBinding,
) (executionsvc.BindOutcome, error) {
	return b.inner.BindAttempt(ctx, request, repositories)
}

func (b bindOnly) ObserveSession(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
) (ports.SessionObservation, domain.WorkflowBindingState, error) {
	return b.inner.ObserveSession(ctx, attemptID)
}

func (b bindOnly) Reconcile(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
) (executionsvc.BindOutcome, error) {
	return b.inner.Reconcile(ctx, attemptID)
}

// fakeBindings is the BindingReader over a fixed table.
type fakeBindings struct {
	bindings map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding
	repos    []domain.WorkflowAttemptRepoBinding
	err      error
}

func (f *fakeBindings) GetWorkflowAttemptBinding(
	_ context.Context,
	attemptID domain.WorkflowAttemptID,
) (domain.WorkflowAttemptBinding, bool, error) {
	if f.err != nil {
		return domain.WorkflowAttemptBinding{}, false, f.err
	}
	binding, ok := f.bindings[attemptID]
	return binding, ok, nil
}

func (f *fakeBindings) ListWorkflowAttemptRepoBindings(
	context.Context,
	domain.WorkflowAttemptID,
) ([]domain.WorkflowAttemptRepoBinding, error) {
	return f.repos, nil
}

// fakeOwnership answers with scripted durable facts, or refuses.
type fakeOwnership struct {
	ownership controllers.AttemptOwnership
	err       error
	// mu guards calls: the concurrent-bind test shares one fake across goroutines.
	mu    sync.Mutex
	calls int
}

func (f *fakeOwnership) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeOwnership) ResolveAttemptOwnership(
	_ context.Context,
	_ domain.WorkflowID,
	_ domain.WorkflowAttemptID,
) (controllers.AttemptOwnership, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return controllers.AttemptOwnership{}, f.err
	}
	return f.ownership, nil
}

func m2AttemptOwnership() controllers.AttemptOwnership {
	now := workflowTestTime()
	return controllers.AttemptOwnership{
		Attempt: domain.WorkflowAttempt{
			ID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1", AttemptNo: 1,
			State: domain.WorkflowAttemptStateClaimed, CreatedAt: now, UpdatedAt: now,
		},
		Task: domain.WorkflowTask{
			ID: "task-1", WorkflowID: "workflow-1", Kind: domain.WorkflowTaskKindDevelopment,
			Title: "Ship the parser", State: domain.WorkflowTaskStateClaimed,
			AgentHarness: domain.HarnessCodex, MaxAttempts: 3, Revision: 1,
			CreatedAt: now, UpdatedAt: now,
		},
		Repos: []domain.WorkflowAttemptRepoBinding{
			{AttemptID: "attempt-1", WorkflowID: "workflow-1", RepoKey: "backend",
				ProjectID: "project-1", ExpectedBaseSHA: "base-1",
				State: domain.WorkflowRepoBindingStatePending},
			{AttemptID: "attempt-1", WorkflowID: "workflow-1", RepoKey: "frontend",
				ProjectID: "project-1", ExpectedBaseSHA: "base-2",
				State: domain.WorkflowRepoBindingStatePending},
		},
		ProjectID: "project-1",
		Harness:   domain.HarnessCodex,
	}
}

func m2Binding() domain.WorkflowAttemptBinding {
	now := workflowTestTime()
	return domain.WorkflowAttemptBinding{
		AttemptID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
		SpawnRequestID: "spawn-attempt-1", SpawnHash: strings.Repeat("d", 64),
		State: domain.WorkflowBindingStateBoundReady, Revision: 2,
		CreatedAt: now, UpdatedAt: now,
	}
}

// testOperatorToken is the operator credential these fixtures accept. Production reads the same
// shape from <dataDir>/operator.token; a fixture holds it directly so no test touches a real data
// dir. A loopback peer alone is NOT the operator (R-04), so every operator call must present it.
const testOperatorToken = "operator-token-for-controller-tests"

// testOperatorTrust is the production operator proof minus the filesystem.
func testOperatorTrust() principal.OperatorTrust {
	return principal.NewTokenTrust(testOperatorToken)
}

// m2Server wires the M2 slots and returns the test server. The principal resolver is the real
// production shape: a loopback peer must ALSO present the operator credential.
func m2Server(
	t *testing.T,
	binder controllers.AttemptBinder,
	bindings controllers.BindingReader,
	ownership controllers.OwnershipResolver,
) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{
		WorkflowBinder:     binder,
		WorkflowBindings:   bindings,
		WorkflowOwnership:  ownership,
		WorkflowPrincipals: principal.NewResolver(nil, nil, testOperatorTrust()),
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// doJSON performs one call as the operator: a loopback peer presenting the operator credential.
func doJSON(t *testing.T, srv *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()
	return doJSONWithHeaders(t, srv, method, path, body,
		map[string]string{principal.HeaderOperatorToken: testOperatorToken})
}

// doJSONAsAnAnonymousPeer performs one call with NO operator credential, for the negative tests
// that must prove the credential (not the peer address) is what authorizes the route.
func doJSONAsAnAnonymousPeer(t *testing.T, srv *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()
	return doJSONWithHeaders(t, srv, method, path, body, nil)
}

func doJSONWithHeaders(
	t *testing.T,
	srv *httptest.Server,
	method, path, body string,
	headers map[string]string,
) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, payload
}

// TestM2BindCarriesDurableOwnership is the B-03 regression: the first bind must send a non-empty
// TaskID, ProjectID, and the task's repositories, all from durable facts.
func TestM2BindCarriesDurableOwnership(t *testing.T) {
	t.Parallel()
	binder := &bindRecorder{outcome: executionsvc.BindOutcome{
		Binding: m2Binding(), Spawned: true, ReasonCode: "ready",
	}}
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{}}
	ownership := &fakeOwnership{ownership: m2AttemptOwnership()}
	srv := m2Server(t, binder, bindings, ownership)

	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	requests := binder.capturedRequests()
	if len(requests) != 1 {
		t.Fatalf("binder calls = %d, want 1", len(requests))
	}
	sent := requests[0]
	if sent.WorkflowID != "workflow-1" || sent.TaskID != "task-1" || sent.AttemptID != "attempt-1" {
		t.Fatalf("spawn identity = %+v", sent)
	}
	if sent.ProjectID != "project-1" {
		t.Fatalf("projectId = %q, want the resolved project", sent.ProjectID)
	}
	if sent.Harness != domain.HarnessCodex {
		t.Fatalf("harness = %q, want the task's harness", sent.Harness)
	}
	if len(sent.Repos) != 2 {
		t.Fatalf("repos = %+v, want the task's two repositories", sent.Repos)
	}
	// And the request must satisfy the port contract the daemon enforces.
	if err := sent.Validate(); err != nil {
		t.Fatalf("the handler sent a spawn request the port refuses: %v", err)
	}
	if len(binder.repos[0]) != 2 || binder.repos[0][0].RepoKey != "backend" {
		t.Fatalf("repositories passed to BindAttempt = %+v", binder.repos[0])
	}
}

// TestM2BindIsIdempotent proves a repeat bind is the same binding and not a second spawn.
func TestM2BindIsIdempotent(t *testing.T) {
	t.Parallel()
	binding := m2Binding()
	binder := &bindRecorder{outcome: executionsvc.BindOutcome{
		Binding: binding, Spawned: false, ReasonCode: "binding_replayed",
	}}
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": binding,
	}}
	srv := m2Server(t, binder, bindings, &fakeOwnership{ownership: m2AttemptOwnership()})

	for attempt := range 2 {
		status, body := doJSON(t, srv, http.MethodPost,
			"/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`)
		if status != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want 200; body=%s", attempt, status, body)
		}
		var decoded controllers.BindWorkflowAttemptResponse
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if decoded.Spawned {
			t.Fatalf("attempt %d reported spawned=true for a replay", attempt)
		}
		if decoded.Binding.AttemptID != "attempt-1" {
			t.Fatalf("attempt %d binding = %+v", attempt, decoded.Binding)
		}
	}
}

// TestM2BindRefusesCrossWorkflow proves an attempt whose durable facts belong to another workflow
// is refused before the binder is ever called.
func TestM2BindRefusesCrossWorkflow(t *testing.T) {
	t.Parallel()
	binder := &bindRecorder{}
	ownership := m2AttemptOwnership()
	ownership.Attempt.WorkflowID = "workflow-other"
	ownership.Task.WorkflowID = "workflow-other"
	srv := m2Server(t, binder, &fakeBindings{}, &fakeOwnership{ownership: ownership})

	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", status, body)
	}
	if !strings.Contains(string(body), "WORKFLOW_BIND_CONFLICT") {
		t.Fatalf("body does not carry the conflict code: %s", body)
	}
	if binder.callCount() != 0 {
		t.Fatal("the binder was called for a cross-workflow bind")
	}
}

// TestM2BindRefusesUnresolvableProject proves a bind with no derivable project is a typed refusal
// and never reaches the binder — the exact shape the B-03 defect had.
func TestM2BindRefusesUnresolvableProject(t *testing.T) {
	t.Parallel()
	binder := &bindRecorder{}
	ownership := m2AttemptOwnership()
	ownership.ProjectID = ""
	srv := m2Server(t, binder, &fakeBindings{}, &fakeOwnership{ownership: ownership})

	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", status, body)
	}
	if !strings.Contains(string(body), "WORKFLOW_PROJECT_UNRESOLVED") {
		t.Fatalf("body does not carry the typed code: %s", body)
	}
	if binder.callCount() != 0 {
		t.Fatal("the binder was called without a resolvable project")
	}
}

// TestM2BindRefusesRepositoryEscalation proves the body can only narrow the task's repo scope.
func TestM2BindRefusesRepositoryEscalation(t *testing.T) {
	t.Parallel()
	binder := &bindRecorder{}
	srv := m2Server(t, binder, &fakeBindings{}, &fakeOwnership{ownership: m2AttemptOwnership()})

	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/bind",
		`{"idempotencyKey":"bind-1","repoKeys":["backend","secrets"]}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", status, body)
	}
	if !strings.Contains(string(body), "WORKFLOW_BIND_CONFLICT") {
		t.Fatalf("body does not carry the conflict code: %s", body)
	}
	if binder.callCount() != 0 {
		t.Fatal("the binder was called for a repository outside the task's scope")
	}
	// The refusal must not enumerate what IS allowed.
	if strings.Contains(string(body), "frontend") {
		t.Fatalf("the refusal disclosed the task's other repositories: %s", body)
	}

	// A narrowing within scope is accepted and passed through.
	status, body = doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/bind",
		`{"idempotencyKey":"bind-2","repoKeys":["backend"]}`)
	if status != http.StatusOK {
		t.Fatalf("narrowed bind status = %d, want 200; body=%s", status, body)
	}
	captured := binder.capturedRequests()
	if len(captured) != 1 {
		t.Fatalf("binder calls = %d, want 1 (the escalation must not reach it)", len(captured))
	}
	if got := captured[0]; len(got.Repos) != 1 || got.Repos[0].RepoKey != "backend" {
		t.Fatalf("narrowed repos = %+v, want only backend", got.Repos)
	}
}

// TestM2BindConcurrentSpawnsOnce proves the handler does not multiply spawn attempts: two
// concurrent binds reach the binder, and the coordinator's own CAS is what makes exactly one of
// them spawn. The handler's job is to send identical, complete requests — which the fake checks —
// so the deduplication decision stays where the durable claim is.
func TestM2BindConcurrentSpawnsOnce(t *testing.T) {
	t.Parallel()
	binding := m2Binding()
	binder := &bindRecorder{outcome: executionsvc.BindOutcome{
		Binding: binding, Spawned: true, ReasonCode: "ready",
	}}
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{}}
	srv := m2Server(t, binder, bindings, &fakeOwnership{ownership: m2AttemptOwnership()})

	const callers = 2
	var wg sync.WaitGroup
	statuses := make([]int, callers)
	wg.Add(callers)
	for i := range callers {
		go func(index int) {
			defer wg.Done()
			status, _ := doJSON(t, srv, http.MethodPost,
				"/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`)
			statuses[index] = status
		}(i)
	}
	wg.Wait()

	for i, status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("caller %d status = %d, want 200", i, status)
		}
	}
	requests := binder.capturedRequests()
	if len(requests) != callers {
		t.Fatalf("binder calls = %d, want %d", len(requests), callers)
	}
	// Both callers must have sent byte-identical spawn facts: the coordinator's spawn-key CAS is
	// only decisive if every racer describes the same spawn.
	for i, request := range requests {
		if request.TaskID != "task-1" || request.ProjectID != "project-1" ||
			request.SpawnKey != "bind-1" || len(request.Repos) != 2 {
			t.Fatalf("caller %d sent %+v", i, request)
		}
	}
}

// TestM2BindingRejectsCrossWorkflowRead is the B-03 read-side fix: an attempt from another
// workflow must answer 404 and disclose no repository data.
func TestM2BindingRejectsCrossWorkflowRead(t *testing.T) {
	t.Parallel()
	bindings := &fakeBindings{
		bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
			"attempt-1": m2Binding(),
		},
		repos: []domain.WorkflowAttemptRepoBinding{
			{AttemptID: "attempt-1", WorkflowID: "workflow-1", RepoKey: "backend",
				ProjectID: "project-1", State: domain.WorkflowRepoBindingStatePending, Revision: 1},
		},
	}
	srv := m2Server(t, &bindRecorder{}, bindings, nil)

	status, body := doJSON(t, srv, http.MethodGet,
		"/api/v1/workflows/workflow-other/attempts/attempt-1/binding", "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", status, body)
	}
	if !strings.Contains(string(body), "WORKFLOW_BINDING_NOT_FOUND") {
		t.Fatalf("body does not carry the not-found code: %s", body)
	}
	for _, leaked := range []string{"backend", "project-1", "spawn-attempt-1"} {
		if strings.Contains(string(body), leaked) {
			t.Fatalf("the refusal leaked %q: %s", leaked, body)
		}
	}

	// The matching workflow still reads.
	status, body = doJSON(t, srv, http.MethodGet,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/binding", "")
	if status != http.StatusOK {
		t.Fatalf("matching read status = %d, want 200; body=%s", status, body)
	}
}

// TestM2ObserveReportsStateAndLiveness covers both halves of the optional liveness check.
func TestM2ObserveReportsStateAndLiveness(t *testing.T) {
	t.Parallel()
	observation := ports.SessionObservation{
		SessionID: "session-1", Readiness: ports.SessionReadinessReady,
		ControllerGeneration: "gen-1", ObservedAt: workflowTestTime(),
	}
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": m2Binding(),
	}}

	// With the optional capability wired, live and reasonCode are present.
	live := &bindRecorder{
		observing: observation, state: domain.WorkflowBindingStateBoundReady,
		live: true, liveReason: "ready",
	}
	srv := m2Server(t, live, bindings, nil)
	status, body := doJSON(t, srv, http.MethodGet,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/observe", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var decoded controllers.ObserveWorkflowAttemptResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.BindingState != "bound_ready" || decoded.Observation == nil ||
		decoded.Observation.SessionID != "session-1" {
		t.Fatalf("observe response = %+v", decoded)
	}
	if decoded.Live == nil || !*decoded.Live || decoded.ReasonCode != "ready" {
		t.Fatalf("liveness = %v/%q, want true/ready", decoded.Live, decoded.ReasonCode)
	}

	// Without it, the same route answers the durable state and omits the liveness fields rather
	// than inventing a false one.
	plain := bindOnly{&bindRecorder{
		observing: observation, state: domain.WorkflowBindingStateBoundReady,
	}}
	srv = m2Server(t, plain, bindings, nil)
	status, body = doJSON(t, srv, http.MethodGet,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/observe", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	decoded = controllers.ObserveWorkflowAttemptResponse{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Live != nil {
		t.Fatalf("a binder without LiveTarget reported live=%v", *decoded.Live)
	}
	if decoded.BindingState != "bound_ready" {
		t.Fatalf("bindingState = %q", decoded.BindingState)
	}
}

// TestM2ObserveRejectsCrossWorkflow proves the same ownership check guards the observe route.
func TestM2ObserveRejectsCrossWorkflow(t *testing.T) {
	t.Parallel()
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": m2Binding(),
	}}
	srv := m2Server(t, &bindRecorder{}, bindings, nil)
	status, body := doJSON(t, srv, http.MethodGet,
		"/api/v1/workflows/workflow-other/attempts/attempt-1/observe", "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", status, body)
	}
}

// TestM2ReconcileAndResumeHappyAndFrozen covers the recovery routes' three outcomes.
func TestM2ReconcileAndResumeHappyAndFrozen(t *testing.T) {
	t.Parallel()
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": m2Binding(),
	}}
	binder := &bindRecorder{
		reconcileOutcome: executionsvc.BindOutcome{
			Binding: m2Binding(), ReasonCode: "ready",
		},
		resumeOutcome: executionsvc.BindOutcome{
			Binding: m2Binding(), ReasonCode: "resumed",
		},
	}
	srv := m2Server(t, binder, bindings, nil)

	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/reconcile", "")
	if status != http.StatusOK {
		t.Fatalf("reconcile status = %d, want 200; body=%s", status, body)
	}
	if !strings.Contains(string(body), `"spawned":false`) {
		t.Fatalf("reconcile reported a spawn: %s", body)
	}

	status, body = doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resume", "")
	if status != http.StatusOK {
		t.Fatalf("resume status = %d, want 200; body=%s", status, body)
	}

	// A frozen reconcile is a 409 with a stable code, never a 200.
	binder.reconcileOutcome = executionsvc.BindOutcome{
		Binding: m2Binding(), Frozen: true, ReasonCode: "reconcile_no_session",
	}
	status, body = doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/reconcile", "")
	if status != http.StatusConflict {
		t.Fatalf("frozen reconcile status = %d, want 409; body=%s", status, body)
	}
	if !strings.Contains(string(body), `"frozen":true`) {
		t.Fatalf("the frozen flag is absent: %s", body)
	}
}

// TestM2ResumeIs501WithoutTheOptionalCapability proves a binder that cannot resume answers 501
// rather than a 200 with an unchanged binding.
func TestM2ResumeIs501WithoutTheOptionalCapability(t *testing.T) {
	t.Parallel()
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": m2Binding(),
	}}
	srv := m2Server(t, bindOnly{&bindRecorder{}}, bindings, nil)

	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resume", "")
	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body=%s", status, body)
	}
	if !strings.Contains(string(body), "NOT_IMPLEMENTED") {
		t.Fatalf("body does not carry the 501 code: %s", body)
	}
}

// TestM2RoutesRefuseUnauthenticatedNonLoopbackPeer proves the principal is enforced at the wire:
// a request whose peer address is not loopback is refused before any service call — even when it
// presents the correct operator credential, because that credential is minted for the loopback
// listener and accepting it from a LAN address would make it a network password.
func TestM2RoutesRefuseUnauthenticatedNonLoopbackPeer(t *testing.T) {
	t.Parallel()
	binder := &bindRecorder{}
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": m2Binding(),
	}}
	ownership := &fakeOwnership{ownership: m2AttemptOwnership()}
	srv := m2Server(t, binder, bindings, ownership)

	for _, route := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`},
		{http.MethodGet, "/api/v1/workflows/workflow-1/attempts/attempt-1/binding", ""},
		{http.MethodGet, "/api/v1/workflows/workflow-1/attempts/attempt-1/observe", ""},
		{http.MethodPost, "/api/v1/workflows/workflow-1/attempts/attempt-1/reconcile", ""},
		{http.MethodPost, "/api/v1/workflows/workflow-1/attempts/attempt-1/resume", ""},
		{http.MethodGet, "/api/v1/workflows/workflow-1/task-requests", ""},
	} {
		var reader io.Reader
		if route.body != "" {
			reader = strings.NewReader(route.body)
		}
		req, err := http.NewRequest(route.method, srv.URL+route.path, reader)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		// httptest rewrites RemoteAddr on its own client; making the request to the same URL but
		// through a transport that reports a LAN peer is not possible with DefaultClient, so the
		// resolver is exercised directly against a crafted request instead.
		recorder := httptest.NewRecorder()
		req.RemoteAddr = "10.1.2.3:4000"
		req.Header.Set(principal.HeaderOperatorToken, testOperatorToken)
		handler := httpd.NewRouterWithControl(config.Config{},
			slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
			httpd.APIDeps{
				WorkflowBinder:     binder,
				WorkflowBindings:   bindings,
				WorkflowOwnership:  ownership,
				WorkflowPrincipals: principal.NewResolver(nil, nil, testOperatorTrust()),
			}, httpd.ControlDeps{})
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s from a LAN peer = %d, want 403; body=%s",
				route.method, route.path, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), "COLLAB_NOT_AUTHORIZED") {
			t.Fatalf("%s %s does not carry the authorization code: %s",
				route.method, route.path, recorder.Body.String())
		}
	}
	if binder.callCount() != 0 || ownership.callCount() != 0 {
		t.Fatalf("a refused caller reached the services: binder=%d ownership=%d",
			binder.callCount(), ownership.callCount())
	}
}

// TestM2AgentDecideIsForbidden proves an agent principal cannot decide its own request even when
// it holds a valid capability and lease, and that an operator decision carries a server-derived
// reference.
func TestM2AgentDecideIsForbidden(t *testing.T) {
	t.Parallel()
	const token = "agent-token"
	sessionID := domain.SessionID("session-1")
	leaseUntil := time.Now().Add(time.Hour)

	sessions := fakePrincipalSessions{sessions: map[domain.SessionID]domain.SessionRecord{
		sessionID: {
			ID: sessionID, ProjectID: "project-1",
			// The worker capability and the agent capability are one credential: AO persists a
			// single verifier per session, so this fixture stores the digest
			// service/browser.Authority produces and the resolver compares against it.
			Metadata: domain.SessionMetadata{
				BrowserCapabilityVerifier: browserVerifierFixture(sessionID, token),
			},
		},
	}}
	attempts := fakePrincipalAttempts{attempts: map[domain.WorkflowAttemptID]domain.WorkflowAttempt{
		"attempt-1": {
			ID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
			LeaseToken: "lease-1", LeaseUntil: &leaseUntil,
		},
	}}
	resolver := principal.StoreResolverWithOperator(sessions, attempts, browserAuthorityFixture{}, testOperatorTrust())

	binding := m2Binding()
	session := sessionID
	binding.SessionID = &session
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": binding,
	}}
	collaboration := &fakeCollaboration{decision: domain.WorkflowTaskRequestDecision{
		Request: domain.WorkflowTaskRequest{
			ID: "req-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
			RequestKind:      domain.WorkflowTaskRequestKindCreateTask,
			RequestSHA:       strings.Repeat("e", 64),
			State:            domain.WorkflowTaskRequestStateRejected,
			WorkflowRevision: 2, Revision: 2,
			CreatedAt: workflowTestTime(), UpdatedAt: workflowTestTime(),
		},
		ReasonCode: "task_request_rejected",
	}}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		WorkflowBindings:      bindings,
		WorkflowCollaboration: collaboration,
		WorkflowPrincipals:    resolver,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	// An agent presenting a valid capability and lease on the decide route is refused.
	req, err := http.NewRequest(http.MethodPost,
		srv.URL+"/api/v1/workflows/workflow-1/task-requests/req-1/decide",
		strings.NewReader(`{"approve":false,"expectedRevision":1}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(principal.HeaderAgentCapability, token)
	req.Header.Set(principal.HeaderAttemptID, "attempt-1")
	req.Header.Set(principal.HeaderAttemptLease, "lease-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent decide status = %d, want 403; body=%s", resp.StatusCode, body)
	}
	if collaboration.lastDecide.RequestID != "" {
		t.Fatalf("an agent decision reached the coordinator: %+v", collaboration.lastDecide)
	}

	// The operator's decision succeeds and its reference is derived, not supplied.
	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/task-requests/req-1/decide",
		`{"approve":false,"expectedRevision":1}`)
	if status != http.StatusOK {
		t.Fatalf("operator decide status = %d, want 200; body=%s", status, body)
	}
	if collaboration.lastDecide.DecidedByRef != "operator:operator" {
		t.Fatalf("decidedByRef = %q, want the server-derived operator ref",
			collaboration.lastDecide.DecidedByRef)
	}
}

// fakePrincipalSessions and fakePrincipalAttempts are the durable readers the real resolver
// needs; they live here rather than in the principal package's tests because this file exercises
// the wiring, not the resolver's own rules.
type fakePrincipalSessions struct {
	sessions map[domain.SessionID]domain.SessionRecord
}

func (f fakePrincipalSessions) GetSession(
	_ context.Context,
	id domain.SessionID,
) (domain.SessionRecord, bool, error) {
	rec, ok := f.sessions[id]
	return rec, ok, nil
}

// browserAuthorityFixture mirrors service/browser.Authority, which is the comparison the daemon
// passes in production. It is defined here so this wire test does not import service/browser.
type browserAuthorityFixture struct{}

func (browserAuthorityFixture) Valid(sessionID domain.SessionID, token, verifier string) bool {
	if sessionID == "" || token == "" || verifier == "" {
		return false
	}
	return browserVerifierFixture(sessionID, token) == verifier
}

func browserVerifierFixture(sessionID domain.SessionID, token string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("ao-browser-capability-verifier-v1\x00"))
	_, _ = h.Write([]byte(sessionID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

type fakePrincipalAttempts struct {
	attempts   map[domain.WorkflowAttemptID]domain.WorkflowAttempt
	bindings   map[domain.SessionID]domain.WorkflowAttemptBinding
	admissions []domain.SchedulerAdmission
	leader     domain.SchedulerLeader
}

func (f fakePrincipalAttempts) GetWorkflowAttemptBindingBySession(_ context.Context, id domain.SessionID) (domain.WorkflowAttemptBinding, bool, error) {
	binding, ok := f.bindings[id]
	return binding, ok, nil
}

func (f fakePrincipalAttempts) ListSchedulerAdmissions(context.Context, bool) ([]domain.SchedulerAdmission, error) {
	return f.admissions, nil
}

func (f fakePrincipalAttempts) GetSchedulerLeader(context.Context) (domain.SchedulerLeader, error) {
	return f.leader, nil
}

func (f fakePrincipalAttempts) GetWorkflowAttempt(
	_ context.Context,
	id domain.WorkflowAttemptID,
) (domain.WorkflowAttempt, bool, error) {
	attempt, ok := f.attempts[id]
	return attempt, ok, nil
}

// TestM2BindSurfacesOwnershipFailures proves the ownership resolver's typed refusals reach the
// wire as classified errors rather than a 500.
func TestM2BindSurfacesOwnershipFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			name:   "missing attempt",
			err:    executionsvc.ErrAttemptNotFound,
			status: http.StatusNotFound,
			code:   "WORKFLOW_ATTEMPT_NOT_FOUND",
		},
		{
			name:   "project unresolved",
			err:    executionsvc.ErrProjectUnresolved,
			status: http.StatusConflict,
			code:   "WORKFLOW_PROJECT_UNRESOLVED",
		},
		{
			name:   "workflow mismatch",
			err:    executionsvc.ErrWorkflowMismatch,
			status: http.StatusConflict,
			code:   "WORKFLOW_BIND_CONFLICT",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			binder := &bindRecorder{}
			srv := m2Server(t, binder, &fakeBindings{}, &fakeOwnership{err: testCase.err})
			status, body := doJSON(t, srv, http.MethodPost,
				"/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`)
			if status != testCase.status {
				t.Fatalf("status = %d, want %d; body=%s", status, testCase.status, body)
			}
			if !strings.Contains(string(body), testCase.code) {
				t.Fatalf("body does not carry %q: %s", testCase.code, body)
			}
			if binder.callCount() != 0 {
				t.Fatal("the binder was called after an ownership refusal")
			}
		})
	}
}

// TestM2BindIs501WithoutOwnership proves the route refuses to guess durable facts.
func TestM2BindIs501WithoutOwnership(t *testing.T) {
	t.Parallel()
	srv := m2Server(t, &bindRecorder{}, &fakeBindings{}, nil)
	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`)
	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body=%s", status, body)
	}
}

// errSensitive is a sentinel used to prove unclassified errors are not rewritten as successes.
var errSensitive = errors.New("sensitive store failure")

// TestM2BindPropagatesUnclassifiedErrors proves an unknown failure is neither reported as a bind
// nor echoed to the caller.
func TestM2BindPropagatesUnclassifiedErrors(t *testing.T) {
	t.Parallel()
	binder := &bindRecorder{bindErr: errSensitive}
	srv := m2Server(t, binder, &fakeBindings{}, &fakeOwnership{ownership: m2AttemptOwnership()})
	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", status, body)
	}
	// The detail stays server-side: an unclassified failure is not a place to echo a store
	// message to the caller, and it must not be reported as a bind either.
	if strings.Contains(string(body), "sensitive store failure") {
		t.Fatalf("the internal failure text was disclosed: %s", body)
	}
}

// TestM2BindFrozenIs409WithBinding proves a frozen bind reports the binding and the reason
// without presenting the outcome as success.
func TestM2BindFrozenIs409WithBinding(t *testing.T) {
	t.Parallel()
	binder := &bindRecorder{outcome: executionsvc.BindOutcome{
		Binding: m2Binding(), Frozen: true, ReasonCode: "spawn_unknown",
	}}
	srv := m2Server(t, binder, &fakeBindings{}, &fakeOwnership{ownership: m2AttemptOwnership()})
	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", status, body)
	}
	if !strings.Contains(string(body), "SPAWN_OUTCOME_UNKNOWN") {
		t.Fatalf("the stable code is absent: %s", body)
	}
	if !strings.Contains(string(body), `"frozen":true`) {
		t.Fatalf("the frozen flag is absent: %s", body)
	}
}

var _ = apierr.Invalid
