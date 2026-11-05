package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	collaborationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/collaboration"
	executionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	interventionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// These tests pin the NEGATIVE authorization and capability chain at the HTTP boundary: a caller
// that is not authorized, or a session whose evidence does not authorize the control, must be
// refused — with zero service calls, not a 200 that quietly did nothing.

// countingGateway is a ports.ExecutionGateway-shaped recorder used through the real intervention
// coordinator. Every method counts, so a test can assert the gateway was never reached.
type countingGateway struct {
	mu               sync.Mutex
	steers           int
	sends            int
	interrupt        int
	inspected        int
	observation      ports.SessionObservation
	inspectErr       error
	receipt          ports.MessageReceipt
	interruptReceipt ports.InterruptReceipt
}

func (g *countingGateway) InspectSession(
	context.Context,
	domain.SessionID,
) (ports.SessionObservation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inspected++
	return g.observation, g.inspectErr
}

func (g *countingGateway) FindSpawn(
	context.Context,
	string,
) (ports.SessionObservation, bool, error) {
	return ports.SessionObservation{}, false, nil
}

func (g *countingGateway) Spawn(
	context.Context,
	ports.SpawnRequest,
) (ports.SessionObservation, error) {
	return ports.SessionObservation{}, ports.ErrExecutionUnsupported
}

func (g *countingGateway) Send(
	_ context.Context,
	request ports.MessageRequest,
) (ports.MessageReceipt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sends++
	receipt := g.receipt
	receipt.SessionID = request.SessionID
	receipt.ClientMessageID = request.ClientMessageID
	return receipt, nil
}

func (g *countingGateway) Steer(
	_ context.Context,
	request ports.MessageRequest,
) (ports.MessageReceipt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.steers++
	receipt := g.receipt
	receipt.SessionID = request.SessionID
	receipt.ClientMessageID = request.ClientMessageID
	return receipt, nil
}

func (g *countingGateway) Interrupt(
	_ context.Context,
	request ports.InterruptRequest,
) (ports.InterruptReceipt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.interrupt++
	receipt := g.interruptReceipt
	receipt.SessionID = request.SessionID
	return receipt, nil
}

func (g *countingGateway) Resume(
	context.Context,
	ports.ResumeRequest,
) (ports.SessionObservation, error) {
	return ports.SessionObservation{}, ports.ErrExecutionUnsupported
}

func (g *countingGateway) counts() (steers, sends, interrupts int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.steers, g.sends, g.interrupt
}

// interventionStore is the minimal persistence the real intervention coordinator drives. Every
// mutation is recorded so a test can prove nothing was stored.
type interventionStore struct {
	mu        sync.Mutex
	created   []domain.WorkflowIntervention
	byMessage map[string]domain.WorkflowIntervention
	openStops int64
	applied   []domain.WorkflowInterventionState
}

func newInterventionStore() *interventionStore {
	return &interventionStore{byMessage: map[string]domain.WorkflowIntervention{}}
}

func (s *interventionStore) CreateWorkflowIntervention(
	_ context.Context,
	intervention domain.WorkflowIntervention,
	_ time.Time,
) (domain.WorkflowIntervention, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = append(s.created, intervention)
	s.byMessage[intervention.ClientMessageID] = intervention
	return intervention, true, nil
}

func (s *interventionStore) CreateWorkflowInterventionIfNoOpenStop(
	ctx context.Context,
	intervention domain.WorkflowIntervention,
	now time.Time,
) (domain.WorkflowIntervention, bool, bool, error) {
	s.mu.Lock()
	blocked := s.openStops > 0
	s.mu.Unlock()
	if blocked {
		return domain.WorkflowIntervention{}, false, true, nil
	}
	stored, created, err := s.CreateWorkflowIntervention(ctx, intervention, now)
	return stored, created, false, err
}

func (s *interventionStore) GetWorkflowIntervention(
	_ context.Context,
	id string,
) (domain.WorkflowIntervention, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, intervention := range s.byMessage {
		if intervention.ID == id {
			return intervention, true, nil
		}
	}
	return domain.WorkflowIntervention{}, false, nil
}

func (s *interventionStore) GetWorkflowInterventionByClientMessage(
	_ context.Context,
	_ domain.WorkflowID,
	clientMessageID string,
) (domain.WorkflowIntervention, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	intervention, ok := s.byMessage[clientMessageID]
	return intervention, ok, nil
}

func (s *interventionStore) ApplyWorkflowInterventionReceipt(
	_ context.Context,
	id string,
	next domain.WorkflowInterventionState,
	_ domain.InterventionChatFact,
	reasonCode string,
	queueAvailable bool,
	_ string,
	_ int64,
	_ time.Time,
) (domain.WorkflowIntervention, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = append(s.applied, next)
	for key, intervention := range s.byMessage {
		if intervention.ID != id {
			continue
		}
		intervention.State = next
		intervention.ReasonCode = reasonCode
		intervention.QueueAvailable = queueAvailable
		s.byMessage[key] = intervention
		return intervention, true, nil
	}
	return domain.WorkflowIntervention{}, false, nil
}

func (s *interventionStore) ListWorkflowInterventionsForAttempt(
	context.Context,
	domain.WorkflowID,
	domain.WorkflowTaskID,
) ([]domain.WorkflowIntervention, error) {
	return nil, nil
}

func (s *interventionStore) CountOpenStopInterventions(
	context.Context,
	domain.WorkflowID,
	domain.WorkflowTaskID,
) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openStops, nil
}

func (s *interventionStore) createdCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.created)
}

func (s *interventionStore) appliedStates() []domain.WorkflowInterventionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.WorkflowInterventionState(nil), s.applied...)
}

// liveBinding is a bound_ready binding with a session and generation, which is what
// resolveTarget requires before it will consider an intervention at all.
func liveBinding(workflowID domain.WorkflowID) domain.WorkflowAttemptBinding {
	binding := m2Binding()
	binding.WorkflowID = workflowID
	session := domain.SessionID("session-1")
	binding.SessionID = &session
	binding.ControllerGeneration = "gen-1"
	return binding
}

// interventionServer wires the REAL intervention coordinator over a counting gateway, so the
// capability gate and the stop gate are exercised through the production chain.
// newStaticOnlyResolver is the production resolver with no probes registered: every answer is
// its display-only static profile, which can never authorize a control.
func newStaticOnlyResolver() *capability.Resolver { return capability.NewResolver() }

func interventionServer(
	t *testing.T,
	binding domain.WorkflowAttemptBinding,
	gateway *countingGateway,
	store *interventionStore,
	resolver *capability.Resolver,
) *httptest.Server {
	t.Helper()
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		binding.AttemptID: binding,
	}}
	coordinator := interventionsvc.New(interventionsvc.Deps{
		Store:    store,
		Bindings: bindings,
		Gateway:  gateway,
		Resolver: resolver,
	})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The harness the gate is asked about comes from the attempt's durable ownership, never from
	// the body, so the fixture wires the same ownership slot the daemon does.
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		WorkflowBindings:      bindings,
		WorkflowInterventions: coordinator,
		WorkflowOwnership:     &fakeOwnership{ownership: m2AttemptOwnership()},
		WorkflowPrincipals:    principal.NewResolver(nil, nil, testOperatorTrust()),
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// TestM2CapabilitiesNeverClaimAnUnprobedSteer proves the capability matrix reports unknown — not
// supported — for evidence nobody collected, and that it exposes no authorization-shaped field.
func TestM2CapabilitiesNeverClaimAnUnprobedSteer(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The production resolver with NO probes registered: every answer is its static profile,
	// which by definition cannot authorize a control.
	deps := httpd.APIDeps{WorkflowCapabilities: capability.NewResolver()}
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
		t.Fatalf("status = %d; body=%s", status, body)
	}
	var decoded controllers.GetWorkflowCapabilitiesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Capabilities) == 0 {
		t.Fatal("the matrix is empty")
	}
	for _, entry := range decoded.Capabilities {
		if entry.Steer == "supported" {
			t.Fatalf("%s reports steer=supported without any probed evidence: %+v", entry.Harness, entry)
		}
		if entry.Source != string(domain.CapabilitySourceStatic) {
			t.Fatalf("%s source = %q, want the static profile", entry.Harness, entry.Source)
		}
		if entry.Expired {
			t.Fatalf("%s reports expired on a freshly built profile", entry.Harness)
		}
	}
	// No field may read as an authorization decision. The wire shape carries evidence and an
	// expiry, never "allowed".
	text := string(body)
	for _, forbidden := range []string{`"allowed"`, `"authorized":true`, `"permitted"`, `"canSteer"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("the capability matrix exposes an authorization field %q: %s", forbidden, text)
		}
	}
	// A static snapshot is display-only and must say so through its source, which is checked
	// above; the reason code is what a client keys on for "why not".
	if !strings.Contains(text, `"steer":"unknown"`) {
		t.Fatalf("no harness reports steer=unknown: %s", text)
	}
}

// TestM2SteerIsRefusedOnStaticEvidence is the capability chain over HTTP: static-only evidence
// must refuse the steer, offer the queue path, and never reach the gateway's Steer.
func TestM2SteerIsRefusedOnStaticEvidence(t *testing.T) {
	t.Parallel()
	gateway := &countingGateway{
		observation: ports.SessionObservation{
			SessionID: "session-1", Readiness: ports.SessionReadinessReady,
			ControllerGeneration: "gen-1",
		},
	}
	srv := interventionServer(t, liveBinding("workflow-1"), gateway, newInterventionStore(),
		capability.NewResolver())

	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		`{"mode":"steer","text":"go left","targetProviderTurnId":"turn-1",`+
			`"clientMessageId":"msg-1","harness":"codex"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", status, body)
	}
	if !strings.Contains(string(body), "STEER_UNSUPPORTED") {
		t.Fatalf("body does not carry STEER_UNSUPPORTED: %s", body)
	}
	if !strings.Contains(string(body), `"queueAvailable":true`) {
		t.Fatalf("the refusal does not offer the queue path: %s", body)
	}
	steers, _, _ := gateway.counts()
	if steers != 0 {
		t.Fatalf("the gateway steered %d times on static-only evidence", steers)
	}
}

// TestM2StopRequiresALiveSession proves the stop gate: a session that is not live must not be
// interrupted, and nothing may be recorded as if it had been.
func TestM2StopRequiresALiveSession(t *testing.T) {
	t.Parallel()
	gateway := &countingGateway{
		// An exited session: bound, but no provider to interrupt.
		observation: ports.SessionObservation{
			SessionID: "session-1", Readiness: ports.SessionReadinessExited,
			ControllerGeneration: "gen-1",
		},
	}
	store := newInterventionStore()
	srv := interventionServer(t, liveBinding("workflow-1"), gateway, store, capability.NewResolver())

	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		`{"mode":"stop","clientMessageId":"stop-1"}`)
	if status == http.StatusOK {
		t.Fatalf("a stop against a dead session answered 200: %s", body)
	}
	_, _, interrupts := gateway.counts()
	if interrupts != 0 {
		t.Fatalf("the gateway interrupted %d times for a dead session", interrupts)
	}
	if !strings.Contains(string(body), "SESSION_NOT_READY") {
		t.Fatalf("body does not carry the session-not-ready code: %s", body)
	}
}

// TestM2QueueAfterConfirmedStopIsRefused proves the durable stop gate: once the binding is
// stopped, a follow-up queue cannot address the turn that was just stopped.
func TestM2QueueAfterConfirmedStopIsRefused(t *testing.T) {
	t.Parallel()
	gateway := &countingGateway{
		observation: ports.SessionObservation{
			SessionID: "session-1", Readiness: ports.SessionReadinessReady,
			ControllerGeneration: "gen-1",
		},
	}
	store := newInterventionStore()
	stopped := liveBinding("workflow-1")
	stopped.State = domain.WorkflowBindingStateStopConfirmed
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		stopped.AttemptID: stopped,
	}}
	coordinator := interventionsvc.New(interventionsvc.Deps{
		Store: store, Bindings: bindings, Gateway: gateway, Resolver: capability.NewResolver(),
	})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		WorkflowBindings:      bindings,
		WorkflowInterventions: coordinator,
		WorkflowPrincipals:    principal.NewResolver(nil, nil, testOperatorTrust()),
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		`{"mode":"queue","text":"more please","clientMessageId":"msg-2"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", status, body)
	}
	_, sends, _ := gateway.counts()
	if sends != 0 {
		t.Fatalf("the gateway sent %d messages after a confirmed stop", sends)
	}
	if store.createdCount() != 0 {
		t.Fatalf("a refused queue was recorded: %d rows", store.createdCount())
	}

	// And the binding is still visibly stopped, which is the durable fact the refusal rests on.
	status, body = doJSON(t, srv, http.MethodGet,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/binding", "")
	if status != http.StatusOK {
		t.Fatalf("binding read status = %d; body=%s", status, body)
	}
	var envelope controllers.WorkflowBindingEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Binding.State != string(domain.WorkflowBindingStateStopConfirmed) {
		t.Fatalf("binding state = %q, want stop_confirmed", envelope.Binding.State)
	}
}

// TestM2AttemptRoutesRefuseCrossWorkflow is the isolation proof for every attempt-scoped route.
//
// The attempt is bound in workflow-1; each request addresses it through workflow-2's URL. Every
// one must refuse with the same stable not-found code and reach no service at all.
func TestM2AttemptRoutesRefuseCrossWorkflow(t *testing.T) {
	t.Parallel()
	gateway := &countingGateway{
		observation: ports.SessionObservation{
			SessionID: "session-1", Readiness: ports.SessionReadinessReady,
			ControllerGeneration: "gen-1",
		},
	}
	store := newInterventionStore()
	binder := &bindRecorder{
		reconcileOutcome: executionsvc.BindOutcome{Binding: m2Binding()},
		resumeOutcome:    executionsvc.BindOutcome{Binding: m2Binding()},
		state:            domain.WorkflowBindingStateBoundReady,
	}
	binding := liveBinding("workflow-1")
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		binding.AttemptID: binding,
	}}
	coordinator := interventionsvc.New(interventionsvc.Deps{
		Store: store, Bindings: bindings, Gateway: gateway, Resolver: capability.NewResolver(),
	})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		WorkflowBindings:      bindings,
		WorkflowBinder:        binder,
		WorkflowInterventions: coordinator,
		WorkflowPrincipals:    principal.NewResolver(nil, nil, testOperatorTrust()),
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	for _, route := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/workflows/workflow-2/attempts/attempt-1/interventions",
			`{"mode":"steer","text":"x","targetProviderTurnId":"turn-1","clientMessageId":"msg-9","harness":"codex"}`},
		{http.MethodGet, "/api/v1/workflows/workflow-2/attempts/attempt-1/observe", ""},
		{http.MethodPost, "/api/v1/workflows/workflow-2/attempts/attempt-1/reconcile", ""},
		{http.MethodPost, "/api/v1/workflows/workflow-2/attempts/attempt-1/resume", ""},
		{http.MethodGet, "/api/v1/workflows/workflow-2/attempts/attempt-1/binding", ""},
	} {
		status, body := doJSON(t, srv, route.method, route.path, route.body)
		if status != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404; body=%s", route.method, route.path, status, body)
		}
		if !strings.Contains(string(body), "WORKFLOW_BINDING_NOT_FOUND") {
			t.Fatalf("%s %s does not carry the not-found code: %s", route.method, route.path, body)
		}
		// The refusal must not disclose anything about the attempt.
		for _, leaked := range []string{"session-1", "gen-1", "spawn-attempt-1"} {
			if strings.Contains(string(body), leaked) {
				t.Fatalf("%s %s leaked %q: %s", route.method, route.path, leaked, body)
			}
		}
	}

	// Nothing external happened, and nothing was recorded.
	steers, sends, interrupts := gateway.counts()
	if steers != 0 || sends != 0 || interrupts != 0 {
		t.Fatalf("a cross-workflow request reached the gateway: steer=%d send=%d interrupt=%d",
			steers, sends, interrupts)
	}
	if store.createdCount() != 0 {
		t.Fatalf("a cross-workflow request wrote %d intervention rows", store.createdCount())
	}
	if binder.callCount() != 0 {
		t.Fatalf("a cross-workflow request reached the binder %d times", binder.callCount())
	}
}

// TestM2DecideRefusesARequestFromAnotherWorkflow proves the request-scoped route is scoped too.
func TestM2DecideRefusesARequestFromAnotherWorkflow(t *testing.T) {
	t.Parallel()
	// The store answers with a request that belongs to workflow-1 while the URL says workflow-2.
	// A store that ignored the workflow scope must not become a cross-workflow decision.
	otherWorkflow := domain.WorkflowTaskRequest{
		ID: "req-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
		RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		RequestJSON: `{}`, RequestSHA: collaborationsvc.RequestSHA("{}"),
		State:            domain.WorkflowTaskRequestStatePending,
		WorkflowRevision: 1, Revision: 1,
		CreatedAt: workflowTestTime(), UpdatedAt: workflowTestTime(),
	}
	// The fake deliberately IGNORES the workflow scope and answers with workflow-1's request, so
	// this test fails unless the CONTROLLER refuses. A scope-aware fake would only prove the store
	// works, which is not what the URL-ownership rule rests on.
	inner := &fakeCollaboration{detail: &otherWorkflow, decision: domain.WorkflowTaskRequestDecision{
		Request: otherWorkflow, ReasonCode: "task_request_rejected",
	}}
	collaboration := &scopeIgnoringCollaboration{fakeCollaboration: inner}
	srv := newCollaborationTestServer(t, collaboration)

	status, body := doJSON(t, srv, http.MethodGet,
		"/api/v1/workflows/workflow-2/task-requests/req-1", "")
	if status != http.StatusNotFound {
		t.Fatalf("cross-workflow detail = %d, want 404; body=%s", status, body)
	}
	if !strings.Contains(string(body), "WORKFLOW_REQUEST_NOT_FOUND") {
		t.Fatalf("cross-workflow detail does not carry the not-found code: %s", body)
	}

	// And a decision against it is refused before the coordinator records anything.
	status, body = doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-2/task-requests/req-1/decide",
		`{"approve":true,"expectedRevision":1}`)
	if status != http.StatusNotFound {
		t.Fatalf("cross-workflow decide = %d, want 404; body=%s", status, body)
	}
	if inner.lastDecide.RequestID != "" {
		t.Fatalf("a cross-workflow decide reached the coordinator: %+v", inner.lastDecide)
	}
}

// TestM2MessagesRefuseACrossWorkflowTarget proves a message may not address another workflow's
// task, and that the refusal writes no row.
func TestM2MessagesRefuseACrossWorkflowTarget(t *testing.T) {
	t.Parallel()
	source := domain.WorkflowTaskRequest{
		ID: "req-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
		RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		RequestJSON: `{}`, RequestSHA: collaborationsvc.RequestSHA("{}"),
		State:            domain.WorkflowTaskRequestStatePending,
		WorkflowRevision: 1, Revision: 1,
		CreatedAt: workflowTestTime(), UpdatedAt: workflowTestTime(),
	}
	_ = source
	// The collaboration coordinator resolves the target task through its Graph, so a target from
	// another workflow is refused by the real service. Here the fake refuses explicitly, which is
	// what pins the controller: it must surface the refusal and store nothing.
	refused := &refusingTargetCollaboration{
		err: domain.ErrCollaborationCrossWorkflow,
	}
	srv := newCollaborationTestServer(t, refused)

	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/messages",
		`{"toTaskId":"task-in-another-workflow","kind":"progress","body":"hello","clientMessageId":"msg-1"}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", status, body)
	}
	if refused.createCalls != 0 {
		t.Fatalf("the coordinator stored %d messages for a cross-workflow target", refused.createCalls)
	}
}

// scopeIgnoringCollaboration answers lookups for ANY workflow, modelling a store that forgot its
// scope clause. Every other method is the ordinary fake.
type scopeIgnoringCollaboration struct {
	*fakeCollaboration
}

func (f *scopeIgnoringCollaboration) GetTaskRequest(
	context.Context,
	domain.WorkflowID,
	string,
) (domain.WorkflowTaskRequest, error) {
	if f.detail == nil {
		return domain.WorkflowTaskRequest{}, domain.ErrWorkflowTaskRequestNotFound
	}
	return *f.detail, nil
}

// refusingTargetCollaboration refuses every create with a fixed error, so a test can prove the
// controller surfaces the refusal instead of writing.
type refusingTargetCollaboration struct {
	fakeCollaboration
	err         error
	createCalls int
}

func (f *refusingTargetCollaboration) CreateMessage(
	context.Context,
	domain.CreateWorkflowMessageInput,
) (collaborationsvc.MessageOutcome, error) {
	f.createCalls++
	return collaborationsvc.MessageOutcome{}, f.err
}

// TestM2AgentCredentialRequiresBothCapabilityAndLease is the item-4 proof at the wire, and the
// resolver's own rule is pinned in the principal package.
func TestM2AgentCredentialRequiresBothCapabilityAndLease(t *testing.T) {
	t.Parallel()
	const token = "agent-token"
	sessionID := domain.SessionID("session-1")
	leaseUntil := time.Now().Add(time.Hour)
	session := sessionID
	binding := m2Binding()
	binding.SessionID = &session

	sessions := fakePrincipalSessions{sessions: map[domain.SessionID]domain.SessionRecord{
		sessionID: {
			ID: sessionID, ProjectID: "project-1",
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
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": binding,
	}}
	gateway := &countingGateway{
		observation: ports.SessionObservation{
			SessionID: sessionID, Readiness: ports.SessionReadinessReady,
			ControllerGeneration: "gen-1",
		},
	}
	store := newInterventionStore()
	coordinator := interventionsvc.New(interventionsvc.Deps{
		Store: store, Bindings: bindings, Gateway: gateway, Resolver: capability.NewResolver(),
	})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		WorkflowBindings:      bindings,
		WorkflowInterventions: coordinator,
		WorkflowPrincipals:    resolver,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	send := func(capabilityToken, leaseToken string) (int, []byte) {
		req, err := http.NewRequest(http.MethodPost,
			srv.URL+"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
			strings.NewReader(`{"mode":"queue","text":"hello","clientMessageId":"msg-1"}`))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(principal.HeaderAgentCapability, capabilityToken)
		req.Header.Set(principal.HeaderAttemptID, "attempt-1")
		if leaseToken != "" {
			req.Header.Set(principal.HeaderAttemptLease, leaseToken)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return resp.StatusCode, body
	}

	// A valid capability with NO lease: refused, and nothing is sent.
	status, body := send(token, "")
	if status != http.StatusForbidden {
		t.Fatalf("capability without a lease = %d, want 403; body=%s", status, body)
	}
	_, sends, _ := gateway.counts()
	if sends != 0 {
		t.Fatal("a capability without a lease reached the gateway")
	}

	// A lease for a different attempt: refused.
	status, body = send(token, "lease-for-another-attempt")
	if status != http.StatusForbidden {
		t.Fatalf("a foreign lease = %d, want 403; body=%s", status, body)
	}

	// A capability for a session that is not this attempt's binding: refused. The resolver is
	// handed the binding's session, so a token minted elsewhere cannot pass.
	otherSessions := fakePrincipalSessions{sessions: map[domain.SessionID]domain.SessionRecord{
		sessionID: {
			ID: sessionID, ProjectID: "project-1",
			Metadata: domain.SessionMetadata{
				BrowserCapabilityVerifier: browserVerifierFixture(sessionID, "other-token"),
			},
		},
	}}
	otherResolver := principal.StoreResolverWithOperator(otherSessions, attempts, browserAuthorityFixture{}, testOperatorTrust())
	otherSrv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{},
		slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{
			WorkflowBindings:      bindings,
			WorkflowInterventions: coordinator,
			WorkflowPrincipals:    otherResolver,
		}, httpd.ControlDeps{}))
	t.Cleanup(otherSrv.Close)
	req, err := http.NewRequest(http.MethodPost,
		otherSrv.URL+"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		strings.NewReader(`{"mode":"queue","text":"hello","clientMessageId":"msg-1"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(principal.HeaderAgentCapability, token)
	req.Header.Set(principal.HeaderAttemptID, "attempt-1")
	req.Header.Set(principal.HeaderAttemptLease, "lease-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign capability = %d, want 403; body=%s", resp.StatusCode, body)
	}

	if _, sends, _ = gateway.counts(); sends != 0 {
		t.Fatalf("a refused credential reached the gateway %d times", sends)
	}
	if store.createdCount() != 0 {
		t.Fatalf("a refused credential wrote %d intervention rows", store.createdCount())
	}
}

// TestM2TaskRequestRedactionAtTheWire is the item-5 proof.
//
// The stored payload carries an absolute path on each platform's shape, a fake secret, and a
// token-shaped hex string. Neither the list nor the detail response may contain ANY of them, in
// addition to the structural assertions (no requestJson key at all).
func TestM2TaskRequestRedactionAtTheWire(t *testing.T) {
	t.Parallel()
	const secret = "sk-live-TOPSECRET-do-not-print"
	const tokenish = "deadbeefdeadbeefdeadbeefdeadbeef" // 32 hex, token-shaped
	storedJSON := `{` +
		`"title":"Expand the parser",` +
		`"notes":"see ` + secret + ` and ` + tokenish + `",` +
		`"paths":["C:\\Users\\someone\\.ssh\\id_rsa","/Users/someone/private","/home/someone/repo"],` +
		`"repoKeys":["backend"],` +
		`"taskCount":1` +
		`}`
	request := domain.WorkflowTaskRequest{
		ID: "req-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
		RequestKind: domain.WorkflowTaskRequestKindExpandRepos,
		RequestJSON: storedJSON, RequestSHA: collaborationsvc.RequestSHA(storedJSON),
		State:            domain.WorkflowTaskRequestStatePending,
		WorkflowRevision: 1, Revision: 1,
		CreatedAt: workflowTestTime(), UpdatedAt: workflowTestTime(),
	}
	svc := &fakeCollaboration{
		request: request, requests: []domain.WorkflowTaskRequest{request}, detail: &request,
	}
	srv := newCollaborationTestServer(t, svc)

	hexOrToken := regexp.MustCompile(`\b[0-9a-f]{32,}\b`)
	// The stored fingerprint is legitimately 64 hex, so it is allowed; everything else is not.
	allowedFingerprint := request.RequestSHA

	for _, path := range []string{
		"/api/v1/workflows/workflow-1/task-requests",
		"/api/v1/workflows/workflow-1/task-requests/req-1",
	} {
		status, body := doJSON(t, srv, http.MethodGet, path, "")
		if status != http.StatusOK {
			t.Fatalf("%s status = %d; body=%s", path, status, body)
		}
		text := string(body)

		// The raw payload, in whole and in fragments, is absent.
		if strings.Contains(text, storedJSON) {
			t.Fatalf("%s returned the raw request JSON verbatim", path)
		}
		if strings.Contains(text, `"requestJson"`) || strings.Contains(text, "request_json") {
			t.Fatalf("%s exposes a requestJson-shaped field: %s", path, text)
		}
		// The secret never appears, in any field.
		if strings.Contains(text, secret) {
			t.Fatalf("%s leaked the fake secret", path)
		}
		// No absolute path survives in any platform's shape.
		for _, leaked := range []string{
			`C:\\`, `C:\Users`, "/Users/someone", "/home/someone", "id_rsa", "private",
		} {
			if strings.Contains(text, leaked) {
				t.Fatalf("%s leaked a path-shaped substring %q: %s", path, leaked, text)
			}
		}
		// No token-shaped hex other than the published fingerprint.
		for _, match := range hexOrToken.FindAllString(text, -1) {
			if match != allowedFingerprint {
				t.Fatalf("%s published an unexplained token-shaped value %q: %s", path, match, text)
			}
		}
		// The legitimate, redacted facts do survive.
		if !strings.Contains(text, `"backend"`) {
			t.Fatalf("%s dropped a legitimate repo key: %s", path, text)
		}
		if !strings.Contains(text, `"summary"`) {
			t.Fatalf("%s returned no summary: %s", path, text)
		}
	}
}
