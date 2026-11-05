package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	collaborationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/collaboration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// fakeCollaboration is a scripted collaboration surface for the wire-contract tests.
type fakeCollaboration struct {
	decision domain.WorkflowTaskRequestDecision
	created  bool
	request  domain.WorkflowTaskRequest
	messages []domain.WorkflowMessage
	requests []domain.WorkflowTaskRequest
	detail   *domain.WorkflowTaskRequest
	// lastDecide captures the derived decider so a wire test can assert the server, not the
	// body, chose it.
	lastDecide collaborationsvc.DecideInput
}

func (f *fakeCollaboration) CreateMessage(
	context.Context,
	domain.CreateWorkflowMessageInput,
) (collaborationsvc.MessageOutcome, error) {
	return collaborationsvc.MessageOutcome{
		Message: domain.WorkflowMessage{ID: "message-1", WorkflowID: "workflow-1"},
		Created: true,
	}, nil
}

func (f *fakeCollaboration) ListMessages(
	context.Context,
	domain.WorkflowID,
	int64, int64,
) ([]domain.WorkflowMessage, error) {
	return f.messages, nil
}

func (f *fakeCollaboration) CreateTaskRequest(
	context.Context,
	collaborationsvc.CreateTaskRequestInput,
) (collaborationsvc.TaskRequestOutcome, error) {
	return collaborationsvc.TaskRequestOutcome{Request: f.request, Created: f.created}, nil
}

func (f *fakeCollaboration) Decide(
	_ context.Context,
	input collaborationsvc.DecideInput,
) (domain.WorkflowTaskRequestDecision, error) {
	f.lastDecide = input
	return f.decision, nil
}

func (f *fakeCollaboration) ListTaskRequests(
	context.Context,
	domain.WorkflowID,
	int64, int64,
) ([]domain.WorkflowTaskRequest, error) {
	return f.requests, nil
}

// GetTaskRequest models a real store: the lookup is scoped to the workflow, so a request that
// lives in another workflow is absent rather than returned. A request the fixture only knows as a
// decision target is still findable, because deciding it implies it exists.
func (f *fakeCollaboration) GetTaskRequest(
	_ context.Context,
	workflowID domain.WorkflowID,
	_ string,
) (domain.WorkflowTaskRequest, error) {
	request := f.detail
	if request == nil && f.decision.Request.ID != "" {
		request = &f.decision.Request
	}
	if request == nil || request.WorkflowID != workflowID {
		return domain.WorkflowTaskRequest{}, domain.ErrWorkflowTaskRequestNotFound
	}
	return *request, nil
}

// newCollaborationTestServer wires only the collaboration slot, which is enough for the
// wire-contract assertions and mirrors how httpd treats an independent M2 dependency.
func newCollaborationTestServer(t *testing.T, svc controllers.CollaborationCoordinator) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The M2 routes derive the caller, so a request that carries no identity at all is refused.
	// These wire-contract tests play the operator by presenting the operator credential from a
	// loopback peer — both facts, which is what TokenOperatorTrust requires: the address alone
	// proves only that some local process is calling.
	deps := httpd.APIDeps{WorkflowCollaboration: svc, WorkflowPrincipals: principal.NewResolver(nil, nil, testOperatorTrust())}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// postAsOperator posts a body from a loopback peer, which is the operator identity these routes
// derive rather than accept from a body.
func postAsOperator(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(principal.HeaderOperatorToken, testOperatorToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return resp
}

// TestTaskRequestResponseCarriesNoRawPayload is the P1-A proof.
//
// The stored request body can name repository scopes and file paths, and only the decide path
// is meant to read it. Two things must hold, and this test pins both:
//
//   - a reader that is not the requester can never obtain the raw JSON, including from the new
//     list and detail routes, which are the surfaces an owner uses to review a pending request;
//   - raising a request requires a proven attempt, so the operator cannot raise one on a task's
//     behalf just because it can reach loopback.
func TestTaskRequestResponseCarriesNoRawPayload(t *testing.T) {
	t.Parallel()
	const storedJSON = `{"repo":"/home/someone/private/repo","paths":["../../etc/passwd"],` +
		`"repoKeys":["backend"],"taskCount":2,"titleHints":["Add a parser"],"budgetDelta":500}`
	stored := domain.WorkflowTaskRequest{
		ID: "req-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
		RequestKind:      domain.WorkflowTaskRequestKindExpandRepos,
		RequestJSON:      storedJSON,
		RequestSHA:       strings.Repeat("a", 64),
		State:            domain.WorkflowTaskRequestStatePending,
		WorkflowRevision: 2, Revision: 1,
		CreatedAt: workflowTestTime(), UpdatedAt: workflowTestTime(),
	}
	svc := &fakeCollaboration{created: true, request: stored, requests: []domain.WorkflowTaskRequest{stored}, detail: &stored}
	srv := newCollaborationTestServer(t, svc)

	// Raising a request as the bare operator is refused: there is no proven attempt behind it.
	createResp := postAsOperator(t, srv.URL+"/api/v1/workflows/workflow-1/task-requests",
		`{"requestId":"req-1","requestKind":"expand_repos","payload":{"repo":"/home/someone/private/repo"}}`)
	defer func() { _ = createResp.Body.Close() }()
	createBody, err := io.ReadAll(createResp.Body)
	if err != nil {
		t.Fatalf("read create body: %v", err)
	}
	if createResp.StatusCode != http.StatusForbidden {
		t.Fatalf("create as operator status = %d, want 403; body=%s", createResp.StatusCode, createBody)
	}

	// The reader surfaces: one request, and a page of them.
	//
	// These are read as the OPERATOR, not as a bare peer: the read routes derive a caller the
	// same way the decide route does, so a request with no operator credential is a 403 and
	// would prove nothing about what the response body carries. doJSON sends the operator
	// credential from a loopback peer, which is the identity these routes accept.
	for _, path := range []string{
		"/api/v1/workflows/workflow-1/task-requests",
		"/api/v1/workflows/workflow-1/task-requests/req-1",
	} {
		status, body := doJSON(t, srv, http.MethodGet, path, "")
		if status != http.StatusOK {
			t.Fatalf("%s status = %d; body=%s", path, status, body)
		}
		text := string(body)
		for _, forbidden := range []string{"requestJson", "request_json", "requestPayload", "payload\":"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s exposed %q: %s", path, forbidden, text)
			}
		}
		for _, leaked := range []string{"/home/someone", "../../etc/passwd", "private/repo"} {
			if strings.Contains(text, leaked) {
				t.Fatalf("%s leaked request content %q: %s", path, leaked, text)
			}
		}
		// The fingerprint and the redacted summary ARE present, so an owner can recognise what
		// they are deciding about without seeing the body.
		if !strings.Contains(text, strings.Repeat("a", 64)) {
			t.Fatalf("%s does not publish the fingerprint: %s", path, text)
		}
		if !strings.Contains(text, `"summary"`) {
			t.Fatalf("%s does not publish a redacted summary: %s", path, text)
		}
		if !strings.Contains(text, `"backend"`) {
			t.Fatalf("%s dropped a legitimate repo key: %s", path, text)
		}
	}
}

// TestDecisionIdentityIsServerDerived proves decidedByRef cannot be supplied by the caller.
//
// The field is gone from the request body, and strict decoding turns an attempt to send it into
// a 400 rather than silently ignoring it: a caller must not believe it recorded an attribution.
func TestDecisionIdentityIsServerDerived(t *testing.T) {
	t.Parallel()
	svc := &fakeCollaboration{
		decision: domain.WorkflowTaskRequestDecision{
			Request: domain.WorkflowTaskRequest{
				ID: "req-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
				RequestKind:      domain.WorkflowTaskRequestKindCreateTask,
				RequestSHA:       strings.Repeat("c", 64),
				State:            domain.WorkflowTaskRequestStateRejected,
				WorkflowRevision: 2, Revision: 2,
				CreatedAt: workflowTestTime(), UpdatedAt: workflowTestTime(),
			},
			ReasonCode: "task_request_rejected",
		},
	}
	srv := newCollaborationTestServer(t, svc)

	forged := postAsOperator(t, srv.URL+"/api/v1/workflows/workflow-1/task-requests/req-1/decide",
		`{"approve":false,"reason":"no","expectedRevision":1,"decidedByRef":"someone-else"}`)
	defer func() { _ = forged.Body.Close() }()
	forgedBody, _ := io.ReadAll(forged.Body)
	if forged.StatusCode != http.StatusBadRequest {
		t.Fatalf("a body decidedByRef status = %d, want 400; body=%s", forged.StatusCode, forgedBody)
	}

	accepted := postAsOperator(t, srv.URL+"/api/v1/workflows/workflow-1/task-requests/req-1/decide",
		`{"approve":false,"reason":"no","expectedRevision":1}`)
	defer func() { _ = accepted.Body.Close() }()
	acceptedBody, _ := io.ReadAll(accepted.Body)
	if accepted.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", accepted.StatusCode, acceptedBody)
	}
	if svc.lastDecide.DecidedByRef != "operator:operator" {
		t.Fatalf("decidedByRef = %q, want the server-derived operator ref",
			svc.lastDecide.DecidedByRef)
	}
	if svc.lastDecide.DecidedBy != domain.WorkflowTaskRequestDeciderHuman {
		t.Fatalf("decidedBy = %q, want human", svc.lastDecide.DecidedBy)
	}
}

// TestApprovedTaskRequestIsNotReportedAsSuccess is the approval-boundary wire proof: an
// approval that created no task must not answer 200, and it must say why in a machine-readable
// field rather than only in prose.
//
// The status is 202 Accepted, not 501: the decision was recorded and is durable, so it is not an
// unimplemented operation — it is an accepted intent whose scheduling has not happened. A client
// that keys on 200 alone would conclude the task exists.
func TestApprovedTaskRequestIsNotReportedAsSuccess(t *testing.T) {
	t.Parallel()
	svc := &fakeCollaboration{
		decision: domain.WorkflowTaskRequestDecision{
			Request: domain.WorkflowTaskRequest{
				ID: "req-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
				RequestKind:      domain.WorkflowTaskRequestKindCreateTask,
				RequestSHA:       strings.Repeat("b", 64),
				State:            domain.WorkflowTaskRequestStateApproved,
				WorkflowRevision: 2, Revision: 2,
				CreatedAt: workflowTestTime(), UpdatedAt: workflowTestTime(),
			},
			Materialization: domain.WorkflowTaskRequestMaterializationPending,
			Materialized:    false,
			ReasonCode:      domain.TaskRequestReasonApprovedNotMaterialized,
		},
	}
	srv := newCollaborationTestServer(t, svc)

	resp := postAsOperator(t, srv.URL+"/api/v1/workflows/workflow-1/task-requests/req-1/decide",
		`{"approve":true,"expectedRevision":1}`)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("an approval that created no task answered 200: %s", body)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", resp.StatusCode, body)
	}
	var decoded controllers.DecideWorkflowTaskRequestResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, body)
	}
	if decoded.Materialization != string(domain.WorkflowTaskRequestMaterializationPending) {
		t.Fatalf("materialization = %q, want pending; body=%s", decoded.Materialization, body)
	}
	if decoded.Materialized {
		t.Fatalf("materialized = true for an approval that created nothing: %s", body)
	}
	if decoded.ReasonCode != domain.TaskRequestReasonApprovedNotMaterialized {
		t.Fatalf("reasonCode = %q, want %q; body=%s",
			decoded.ReasonCode, domain.TaskRequestReasonApprovedNotMaterialized, body)
	}
	if !strings.Contains(decoded.Message, "No task was created") {
		t.Fatalf("message does not say no task was created: %q", decoded.Message)
	}
}

// TestRejectedTaskRequestIsAnOrdinarySuccess proves 202 is specific to a pending approval, not to
// decisions in general: a rejection is a completed decision with nothing left to do.
func TestRejectedTaskRequestIsAnOrdinarySuccess(t *testing.T) {
	t.Parallel()
	svc := &fakeCollaboration{
		decision: domain.WorkflowTaskRequestDecision{
			Request: domain.WorkflowTaskRequest{
				ID: "req-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
				RequestKind:      domain.WorkflowTaskRequestKindCreateTask,
				RequestSHA:       strings.Repeat("c", 64),
				State:            domain.WorkflowTaskRequestStateRejected,
				WorkflowRevision: 2, Revision: 2,
				CreatedAt: workflowTestTime(), UpdatedAt: workflowTestTime(),
			},
			Materialization: domain.WorkflowTaskRequestMaterializationNotApplicable,
			Materialized:    false,
			ReasonCode:      domain.TaskRequestReasonRejected,
		},
	}
	srv := newCollaborationTestServer(t, svc)

	resp := postAsOperator(t, srv.URL+"/api/v1/workflows/workflow-1/task-requests/req-1/decide",
		`{"approve":false,"expectedRevision":1}`)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var decoded controllers.DecideWorkflowTaskRequestResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, body)
	}
	if decoded.Materialization != string(domain.WorkflowTaskRequestMaterializationNotApplicable) {
		t.Fatalf("materialization = %q, want not_applicable; body=%s", decoded.Materialization, body)
	}
	if decoded.Materialized {
		t.Fatalf("materialized = true for a rejection: %s", body)
	}
}

// TestWorkflowStaticRoutesPrecede is the route-ordering regression test required by the design.
//
// chi v5.1.0 matches node types in a fixed order with ntStatic before ntParam, so
// /workflows/capabilities cannot be swallowed by /workflows/{workflowId}. This test pins that
// behaviourally — including for a static segment that is itself a syntactically valid workflow
// id — so a future router change cannot silently reroute the capability matrix into a workflow
// lookup.
func TestWorkflowStaticRoutesPrecede(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Only the capability slot is wired: if the static route were swallowed by the parameterised
	// one, the request would land on the workflow-definition handler instead and answer 501 with
	// the OTHER operation. The 200 with a capabilities body is the discriminator.
	deps := httpd.APIDeps{WorkflowCapabilities: &stubCapabilities{}}
	approvals := principal.NewApprovalStore(principal.ApprovalStoreOptions{})
	deps.OperatorApprovals = approvals
	consent := &recordingConsent{}
	deps.OperatorConsent = consent
	deps.WorkflowPrincipals = principal.NewResolver(nil, nil, principal.ApprovalOperatorTrust{Approvals: approvals})
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	env := &approvalEnv{srv: srv, consent: consent}
	status, body := env.approvedAction(t, http.MethodGet, "/api/v1/workflows/capabilities", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the static route must win); body=%s", status, body)
	}
	if !strings.Contains(string(body), "capabilities") {
		t.Fatalf("the request did not reach the capability handler: %s", body)
	}

	// A path that is a syntactically valid workflow id still routes to the workflow handler,
	// proving the static route took only its own segment.
	workflowResp, err := http.Get(srv.URL + "/api/v1/workflows/workflow-abc123")
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	defer func() { _ = workflowResp.Body.Close() }()
	workflowBody, _ := io.ReadAll(workflowResp.Body)
	if strings.Contains(string(workflowBody), "capabilities") {
		t.Fatalf("a workflow lookup was routed to the capability handler: %s", workflowBody)
	}
}

// stubCapabilities answers the capability route without a resolver.
type stubCapabilities struct{}

func (s *stubCapabilities) Resolve(
	context.Context,
	ports.CapabilityProbeRequest,
) (domain.HarnessCapabilitySnapshot, error) {
	return domain.NewHarnessCapabilitySnapshot(
		domain.HarnessCodex, domain.CapabilitySourceStatic, workflowTestTime(), time.Minute,
	), nil
}
