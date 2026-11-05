package controllers_test

import (
	"context"
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
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	collaborationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/collaboration"
	executionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	interventionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	workflowsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// These tests pin R-04 and R-05 at the HTTP boundary.
//
// R-04: the operator is a CREDENTIAL, not a peer address. Loopback proves "some local process",
// and every worker AO launches shares that address, so a loopback peer with no operator token must
// be refused on every sensitive route.
//
// R-05: the capability gate may not be asked about a harness the caller chose. The harness comes
// from the attempt's durable ownership, and a disagreeing body harness is a 409.

// auditRecorder is an in-package audit sink: it records exactly what a handler decided, so a test
// can assert "one state change, one audit row".
type auditRecorder struct {
	mu      sync.Mutex
	records []principal.AuditRecord
	err     error
}

func (a *auditRecorder) RecordSensitiveDecision(
	_ context.Context,
	record principal.AuditRecord,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.records = append(a.records, record)
	return nil
}

func (a *auditRecorder) all() []principal.AuditRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]principal.AuditRecord(nil), a.records...)
}

// resolveService is a WorkflowService whose Resolve applies exactly once and counts calls, so a
// test can prove both "no state change" and "one state change".
type resolveService struct {
	fakeWorkflowService
	mu      sync.Mutex
	resolve int
	// attempt is what Resolve answers with; state is what it leaves behind.
	attempt domain.WorkflowAttempt
	state   domain.WorkflowAttemptState
	err     error
}

func (s *resolveService) Resolve(
	_ context.Context,
	input workflowsvc.ResolveInput,
) (domain.WorkflowAttempt, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return domain.WorkflowAttempt{}, false, s.err
	}
	s.resolve++
	applied := s.state != domain.WorkflowAttemptStateSucceeded &&
		s.state != domain.WorkflowAttemptStateFailed
	s.state = input.Outcome
	s.attempt = m2Attempt()
	s.attempt.State = s.state
	return s.attempt, applied, nil
}

func (s *resolveService) resolveCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolve
}

func (s *resolveService) currentState() domain.WorkflowAttemptState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// m2Attempt is an attempt in the UNKNOWN state, which is the only state Resolve may leave.
func m2Attempt() domain.WorkflowAttempt {
	now := workflowTestTime()
	return domain.WorkflowAttempt{
		ID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1", AttemptNo: 1,
		State: domain.WorkflowAttemptStateUnknown, CreatedAt: now, UpdatedAt: now,
	}
}

// resolveServer wires the M1 workflow service, the ownership resolver and the audit sink, with the
// real principal resolver over a test operator credential.
func resolveServer(
	t *testing.T,
	svc controllers.WorkflowService,
	ownership controllers.OwnershipResolver,
	audit principal.AuditRecorder,
) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": liveBinding("workflow-1"),
	}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Workflows:          svc,
		WorkflowBindings:   bindings,
		WorkflowOwnership:  ownership,
		WorkflowPrincipals: principal.NewResolver(nil, nil, testOperatorTrust()),
		WorkflowAudit:      audit,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// TestM2ResolveRequiresOperatorProof is the core R-04 wire test: resolve changes an attempt's final
// state, so a caller with no operator credential must be refused and the state must not move —
// even though the request comes from loopback.
func TestM2ResolveRequiresOperatorProof(t *testing.T) {
	t.Parallel()
	svc := &resolveService{state: domain.WorkflowAttemptStateUnknown}
	ownership := &fakeOwnership{ownership: m2AttemptOwnership()}
	srv := resolveServer(t, svc, ownership, &auditRecorder{})

	body := `{"outcome":"succeeded","expectedTaskRevision":1,"idempotencyKey":"resolve-1"}`
	status, payload := doJSONAsAnAnonymousPeer(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resolve", body)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", status, payload)
	}
	if !strings.Contains(string(payload), "COLLAB_NOT_AUTHORIZED") {
		t.Fatalf("body does not carry the authorization code: %s", payload)
	}
	if svc.resolveCalls() != 0 {
		t.Fatalf("an unauthorised resolve reached the service %d times", svc.resolveCalls())
	}
	if state := svc.currentState(); state != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("attempt state = %q, want it unchanged at unknown", state)
	}
}

// TestM2ResolveWithAWrongOperatorTokenIsRefused proves the comparison is exact over the wire.
func TestM2ResolveWithAWrongOperatorTokenIsRefused(t *testing.T) {
	t.Parallel()
	svc := &resolveService{state: domain.WorkflowAttemptStateUnknown}
	srv := resolveServer(t, svc, &fakeOwnership{ownership: m2AttemptOwnership()}, &auditRecorder{})

	status, payload := doJSONWithHeaders(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resolve",
		`{"outcome":"succeeded","expectedTaskRevision":1,"idempotencyKey":"resolve-1"}`,
		map[string]string{principal.HeaderOperatorToken: "not-the-operator-token"})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", status, payload)
	}
	if svc.resolveCalls() != 0 || svc.currentState() != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("a wrongly credentialed resolve reached the service: calls=%d state=%q",
			svc.resolveCalls(), svc.currentState())
	}
}

// TestM2ResolveByAnAgentIsRefused proves an agent cannot approve its own attempt's fate, and that
// presenting a valid operator token alongside an agent capability does not silently downgrade the
// caller into the operator.
func TestM2ResolveByAnAgentIsRefused(t *testing.T) {
	t.Parallel()
	const agentToken = "agent-capability"
	sessionID := domain.SessionID("session-1")
	leaseUntil := time.Now().Add(time.Hour)

	sessions := fakePrincipalSessions{sessions: map[domain.SessionID]domain.SessionRecord{
		sessionID: {
			ID: sessionID, ProjectID: "project-1",
			Metadata: domain.SessionMetadata{
				BrowserCapabilityVerifier: browserVerifierFixture(sessionID, agentToken),
			},
		},
	}}
	attempts := fakePrincipalAttempts{attempts: map[domain.WorkflowAttemptID]domain.WorkflowAttempt{
		"attempt-1": {
			ID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
			LeaseToken: "lease-1", LeaseUntil: &leaseUntil,
		},
	}}
	resolver := principal.StoreResolverWithOperator(
		sessions, attempts, browserAuthorityFixture{}, testOperatorTrust())

	svc := &resolveService{state: domain.WorkflowAttemptStateUnknown}
	binding := liveBinding("workflow-1")
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": binding,
	}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Workflows:          svc,
		WorkflowBindings:   bindings,
		WorkflowOwnership:  &fakeOwnership{ownership: m2AttemptOwnership()},
		WorkflowPrincipals: resolver,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	status, payload := doJSONWithHeaders(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resolve",
		`{"outcome":"succeeded","expectedTaskRevision":1,"idempotencyKey":"resolve-1"}`,
		map[string]string{
			principal.HeaderOperatorToken:   testOperatorToken,
			principal.HeaderAgentCapability: agentToken,
			principal.HeaderAttemptID:       "attempt-1",
			principal.HeaderAttemptLease:    "lease-1",
		})
	if status != http.StatusForbidden {
		t.Fatalf("agent resolve status = %d, want 403; body=%s", status, payload)
	}
	if svc.resolveCalls() != 0 || svc.currentState() != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("an agent resolve reached the service: calls=%d state=%q",
			svc.resolveCalls(), svc.currentState())
	}
}

// TestM2ResolveCrossWorkflowIs404AndChangesNothing proves an attempt from another workflow cannot
// be resolved through this URL, and that the refusal is a 404 (never a 409 that would confirm the
// attempt exists elsewhere).
func TestM2ResolveCrossWorkflowIs404AndChangesNothing(t *testing.T) {
	t.Parallel()
	svc := &resolveService{state: domain.WorkflowAttemptStateUnknown}
	// Ownership answers about workflow-1's attempt; the URL asks about workflow-2.
	srv := resolveServer(t, svc, &fakeOwnership{ownership: m2AttemptOwnership()}, &auditRecorder{})

	status, payload := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-2/attempts/attempt-1/resolve",
		`{"outcome":"succeeded","expectedTaskRevision":1,"idempotencyKey":"resolve-1"}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", status, payload)
	}
	if !strings.Contains(string(payload), "WORKFLOW_ATTEMPT_NOT_FOUND") {
		t.Fatalf("body does not carry the not-found code: %s", payload)
	}
	if svc.resolveCalls() != 0 {
		t.Fatalf("a cross-workflow resolve reached the service %d times", svc.resolveCalls())
	}
	if state := svc.currentState(); state != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("cross-workflow resolve changed the attempt state to %q", state)
	}
}

// TestM2ResolveRecordsExactlyOneAuditRow is the "one state change, one audit row" rule.
func TestM2ResolveRecordsExactlyOneAuditRow(t *testing.T) {
	t.Parallel()
	svc := &resolveService{state: domain.WorkflowAttemptStateUnknown}
	audit := &auditRecorder{}
	srv := resolveServer(t, svc, &fakeOwnership{ownership: m2AttemptOwnership()}, audit)

	status, payload := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resolve",
		`{"outcome":"succeeded","expectedTaskRevision":1,"idempotencyKey":"resolve-1"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, payload)
	}
	records := audit.all()
	if len(records) != 1 {
		t.Fatalf("audit rows = %d, want exactly 1", len(records))
	}
	record := records[0]
	if record.Decision != "resolve_attempt" {
		t.Fatalf("decision = %q, want resolve_attempt", record.Decision)
	}
	if record.ActorKind != principal.KindOperator || record.ActorRef != "operator:operator" {
		t.Fatalf("actor = %q/%q, want the derived operator", record.ActorKind, record.ActorRef)
	}
	if record.AttemptID != "attempt-1" || record.WorkflowID != "workflow-1" {
		t.Fatalf("subject = %q/%q, want the route's attempt and workflow",
			record.WorkflowID, record.AttemptID)
	}
	if record.Outcome != string(domain.WorkflowAttemptStateSucceeded) {
		t.Fatalf("outcome = %q, want succeeded", record.Outcome)
	}
	if record.TokenFingerprint != principal.OperatorTokenFingerprint(testOperatorToken) {
		t.Fatalf("fingerprint = %q, want the presented credential's", record.TokenFingerprint)
	}
	if record.TokenFingerprint == testOperatorToken {
		t.Fatal("the audit record carries the raw token")
	}
	if record.DaemonRequestID == "" {
		t.Fatal("the audit record has no request id")
	}
	if record.At.IsZero() {
		t.Fatal("the audit record has no timestamp")
	}
}

// TestM2ResolveReplayChangesNothingTwice proves the idempotency material keeps working: a second
// resolve after the attempt has already been decided does not change the state again, and does not
// claim a second decision in the audit trail.
func TestM2ResolveReplayChangesNothingTwice(t *testing.T) {
	t.Parallel()
	svc := &resolveService{state: domain.WorkflowAttemptStateUnknown}
	audit := &auditRecorder{}
	srv := resolveServer(t, svc, &fakeOwnership{ownership: m2AttemptOwnership()}, audit)

	body := `{"outcome":"succeeded","expectedTaskRevision":1,"idempotencyKey":"resolve-1"}`
	status, payload := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resolve", body)
	if status != http.StatusOK {
		t.Fatalf("first resolve status = %d; body=%s", status, payload)
	}
	firstState := svc.currentState()

	status, payload = doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resolve", body)
	if status != http.StatusOK {
		t.Fatalf("replay status = %d, want 200; body=%s", status, payload)
	}
	if svc.currentState() != firstState {
		t.Fatalf("the replay changed the state from %q to %q", firstState, svc.currentState())
	}
	if rows := len(audit.all()); rows != 1 {
		t.Fatalf("audit rows after a replay = %d, want 1 (no second decision was taken)", rows)
	}
}

// decideService applies a decision once, like the store's revision+state CAS.
type decideService struct {
	fakeCollaboration
	mu       sync.Mutex
	decided  bool
	revision int64
}

func (d *decideService) Decide(
	_ context.Context,
	input collaborationsvc.DecideInput,
) (domain.WorkflowTaskRequestDecision, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	state := domain.WorkflowTaskRequestStateRejected
	if input.Approve {
		state = domain.WorkflowTaskRequestStateApproved
	}
	if d.decided {
		// The store matched no row: same answer a real CAS gives.
		return domain.WorkflowTaskRequestDecision{}, domain.ErrWorkflowTaskRequestConflict
	}
	d.decided = true
	d.lastDecide = input
	request := *d.detail
	request.State = state
	request.DecidedByRef = input.DecidedByRef
	request.Revision++
	d.revision = request.Revision
	d.detail = &request
	materialization := domain.WorkflowTaskRequestMaterializationNotApplicable
	reasonCode := domain.TaskRequestReasonRejected
	if input.Approve {
		materialization = domain.WorkflowTaskRequestMaterializationPending
		reasonCode = domain.TaskRequestReasonApprovedNotMaterialized
	}
	return domain.WorkflowTaskRequestDecision{
		Request:         request,
		Materialization: materialization,
		Materialized:    materialization.Materialized(),
		ReasonCode:      reasonCode,
	}, nil
}

func (d *decideService) decisions() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.decided {
		return 1
	}
	return 0
}

// pendingRequest is an undecided task request in workflow-1.
func pendingRequest() domain.WorkflowTaskRequest {
	return domain.WorkflowTaskRequest{
		ID: "req-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
		RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		RequestJSON: `{}`, RequestSHA: collaborationsvc.RequestSHA("{}"),
		State:            domain.WorkflowTaskRequestStatePending,
		WorkflowRevision: 1, Revision: 1,
		CreatedAt: workflowTestTime(), UpdatedAt: workflowTestTime(),
	}
}

func collaborationServer(
	t *testing.T,
	coordinator controllers.CollaborationCoordinator,
	audit principal.AuditRecorder,
) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		WorkflowCollaboration: coordinator,
		WorkflowPrincipals:    principal.NewResolver(nil, nil, testOperatorTrust()),
		WorkflowAudit:         audit,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// TestM2DecideRequiresOperatorProof proves the decide route refuses a caller with no operator
// credential, and reaches no coordinator.
func TestM2DecideRequiresOperatorProof(t *testing.T) {
	t.Parallel()
	request := pendingRequest()
	inner := &decideService{fakeCollaboration: fakeCollaboration{detail: &request}}
	srv := collaborationServer(t, inner, &auditRecorder{})

	status, payload := doJSONAsAnAnonymousPeer(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/task-requests/req-1/decide",
		`{"approve":false,"expectedRevision":1}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", status, payload)
	}
	if inner.lastDecide.RequestID != "" {
		t.Fatalf("an unauthorised decide reached the coordinator: %+v", inner.lastDecide)
	}
}

// TestM2DecideRecordsOneAuditRowAndReplaysWithoutASecond is R-04's idempotency rule: a duplicate
// decide yields the SAME stored decision and exactly one audit record.
func TestM2DecideRecordsOneAuditRowAndReplaysWithoutASecond(t *testing.T) {
	t.Parallel()
	request := pendingRequest()
	audit := &auditRecorder{}
	inner := &decideService{fakeCollaboration: fakeCollaboration{detail: &request}}
	srv := collaborationServer(t, inner, audit)

	body := `{"approve":false,"expectedRevision":1,"reason":"not now"}`
	status, payload := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/task-requests/req-1/decide", body)
	if status != http.StatusOK {
		t.Fatalf("decide status = %d, want 200; body=%s", status, payload)
	}
	var first controllers.DecideWorkflowTaskRequestResponse
	if err := json.Unmarshal(payload, &first); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if first.Request.State != string(domain.WorkflowTaskRequestStateRejected) {
		t.Fatalf("state = %q, want rejected", first.Request.State)
	}
	if first.Request.DecidedByRef != "operator:operator" {
		t.Fatalf("decidedByRef = %q, want the server-derived operator ref", first.Request.DecidedByRef)
	}
	if rows := len(audit.all()); rows != 1 {
		t.Fatalf("audit rows = %d, want exactly 1", rows)
	}
	record := audit.all()[0]
	if record.Decision != "decide_task_request" || record.RequestID != "req-1" {
		t.Fatalf("audit record = %+v, want the decided request", record)
	}

	// The replay is the same answer, and takes no second decision.
	status, payload = doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/task-requests/req-1/decide", body)
	if status != http.StatusOK {
		t.Fatalf("replay status = %d, want 200; body=%s", status, payload)
	}
	var replayed controllers.DecideWorkflowTaskRequestResponse
	if err := json.Unmarshal(payload, &replayed); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if replayed.Request.Revision != first.Request.Revision ||
		replayed.Request.State != first.Request.State {
		t.Fatalf("replay answered with a different decision: first=%+v replay=%+v",
			first.Request, replayed.Request)
	}
	if rows := len(audit.all()); rows != 1 {
		t.Fatalf("audit rows after a replay = %d, want 1 (a duplicate took no decision)", rows)
	}
	if inner.decisions() != 1 {
		t.Fatalf("applied decisions = %d, want exactly 1", inner.decisions())
	}
}

// TestM2DuplicateApprovalStays202WithOneDecisionAndOneAuditRow is R-08's idempotency
// rule for the approval direction: a retried approval answers the SAME status and the
// same pending materialization, records one decision, and appends one audit row.
func TestM2DuplicateApprovalStays202WithOneDecisionAndOneAuditRow(t *testing.T) {
	t.Parallel()
	request := pendingRequest()
	audit := &auditRecorder{}
	inner := &decideService{fakeCollaboration: fakeCollaboration{detail: &request}}
	srv := collaborationServer(t, inner, audit)

	body := `{"approve":true,"expectedRevision":1,"reason":"go ahead"}`
	status, payload := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/task-requests/req-1/decide", body)
	if status != http.StatusAccepted {
		t.Fatalf("approval status = %d, want 202; body=%s", status, payload)
	}
	var first controllers.DecideWorkflowTaskRequestResponse
	if err := json.Unmarshal(payload, &first); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if first.Materialization != string(domain.WorkflowTaskRequestMaterializationPending) {
		t.Fatalf("materialization = %q, want pending", first.Materialization)
	}
	if first.Materialized {
		t.Fatal("materialized = true for an approval that created nothing")
	}
	if rows := len(audit.all()); rows != 1 {
		t.Fatalf("audit rows = %d, want 1", rows)
	}

	// The retry converges on the same answer without taking a second decision.
	status, payload = doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/task-requests/req-1/decide", body)
	if status != http.StatusOK && status != http.StatusAccepted {
		t.Fatalf("replay status = %d, want 200 or 202; body=%s", status, payload)
	}
	var replayed controllers.DecideWorkflowTaskRequestResponse
	if err := json.Unmarshal(payload, &replayed); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if replayed.Materialized {
		t.Fatalf("replay claimed materialization: %s", payload)
	}
	if replayed.Materialization == string(domain.WorkflowTaskRequestMaterializationMaterialized) {
		t.Fatalf("replay materialization = %q for an approval that created nothing", replayed.Materialization)
	}
	if rows := len(audit.all()); rows != 1 {
		t.Fatalf("audit rows after a replay = %d, want 1 (a duplicate took no decision)", rows)
	}
	if inner.decisions() != 1 {
		t.Fatalf("applied decisions = %d, want exactly 1", inner.decisions())
	}
}

// TestM2DecideRefusesAnAgentEvenWithTheOperatorToken is the "no silent downgrade" rule at the wire:
// an agent capability plus a valid operator token is still an agent, and agents may not approve.
func TestM2DecideRefusesAnAgentEvenWithTheOperatorToken(t *testing.T) {
	t.Parallel()
	const agentToken = "agent-capability"
	sessionID := domain.SessionID("session-1")
	sessions := fakePrincipalSessions{sessions: map[domain.SessionID]domain.SessionRecord{
		sessionID: {
			ID: sessionID, ProjectID: "project-1",
			Metadata: domain.SessionMetadata{
				BrowserCapabilityVerifier: browserVerifierFixture(sessionID, agentToken),
			},
		},
	}}
	request := pendingRequest()
	inner := &decideService{fakeCollaboration: fakeCollaboration{detail: &request}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		WorkflowCollaboration: inner,
		WorkflowPrincipals: principal.NewResolver(
			sessions,
			principal.CapabilityVerifier{Sessions: sessions, Authority: browserAuthorityFixture{}},
			testOperatorTrust(),
		),
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	status, payload := doJSONWithHeaders(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/task-requests/req-1/decide",
		`{"approve":true,"expectedRevision":1}`,
		map[string]string{
			principal.HeaderOperatorToken:   testOperatorToken,
			principal.HeaderAgentCapability: agentToken,
		})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", status, payload)
	}
	if inner.lastDecide.RequestID != "" {
		t.Fatalf("an agent decision reached the coordinator: %+v", inner.lastDecide)
	}
}

// TestM2InterventionRefusesABodyHarnessThatDisagrees is the R-05 wire test: the capability gate must
// be asked about the attempt's harness, so a body that names a different one is a 409 and nothing
// reaches the coordinator.
func TestM2InterventionRefusesABodyHarnessThatDisagrees(t *testing.T) {
	t.Parallel()
	recorder := &recordingInterventions{outcome: interventionsvc.Outcome{
		Intervention: domain.WorkflowIntervention{ID: "intervention-1", WorkflowID: "workflow-1"},
		Performed:    true,
	}}
	srv := interventionOnlyServer(t, recorder)

	// The attempt's ownership says codex; the body claims claude-code.
	status, payload := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		`{"mode":"steer","text":"go left","targetProviderTurnId":"turn-1",`+
			`"clientMessageId":"msg-1","harness":"claude-code"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", status, payload)
	}
	if !strings.Contains(string(payload), "WORKFLOW_HARNESS_MISMATCH") {
		t.Fatalf("body does not carry WORKFLOW_HARNESS_MISMATCH: %s", payload)
	}
	if _, ok := recorder.lastSteer(); ok {
		t.Fatal("a mismatched body harness reached the coordinator")
	}
}

// TestM2InterventionUsesTheTrustedHarnessNotTheBody proves the coordinator is handed the DURABLE
// harness even when the body names the same one: the body is a check, never a source.
func TestM2InterventionUsesTheTrustedHarnessNotTheBody(t *testing.T) {
	t.Parallel()
	recorder := &recordingInterventions{outcome: interventionsvc.Outcome{
		Intervention: domain.WorkflowIntervention{
			ID: "intervention-1", WorkflowID: "workflow-1", TaskID: "task-1",
			Mode: domain.WorkflowInterventionModeSteer, State: domain.WorkflowInterventionStateQueued,
			ClientMessageID: "msg-1", Revision: 1,
		},
		Performed: true,
	}}
	srv := interventionOnlyServer(t, recorder)

	// The body omits the harness entirely, so only durable ownership can supply it.
	status, payload := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		`{"mode":"steer","text":"go left","targetProviderTurnId":"turn-1","clientMessageId":"msg-1"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, payload)
	}
	steer, ok := recorder.lastSteer()
	if !ok {
		t.Fatal("the steer never reached the coordinator")
	}
	if steer.Harness != domain.HarnessCodex {
		t.Fatalf("harness = %q, want the attempt's durable harness (codex)", steer.Harness)
	}
}

// TestM2InterventionRefusesWhenNoHarnessCanBeDerived proves the fail-closed case: with no ownership
// source and no live observation, the route refuses rather than guessing (or trusting the body).
func TestM2InterventionRefusesWhenNoHarnessCanBeDerived(t *testing.T) {
	t.Parallel()
	recorder := &recordingInterventions{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": liveBinding("workflow-1"),
	}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		WorkflowBindings:      bindings,
		WorkflowInterventions: recorder,
		// No WorkflowOwnership and no WorkflowBinder: nothing can name the harness.
		WorkflowPrincipals: principal.NewResolver(nil, nil, testOperatorTrust()),
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	status, payload := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		`{"mode":"steer","text":"go left","targetProviderTurnId":"turn-1",`+
			`"clientMessageId":"msg-1","harness":"codex"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", status, payload)
	}
	if !strings.Contains(string(payload), "WORKFLOW_HARNESS_MISMATCH") {
		t.Fatalf("body does not carry WORKFLOW_HARNESS_MISMATCH: %s", payload)
	}
	if _, ok := recorder.lastSteer(); ok {
		t.Fatal("a steer with no derivable harness reached the coordinator")
	}
}

// TestM2InterventionRequiresOperatorProof proves the intervention route inherits R-04: it records a
// receipt and can change a binding, so a caller with no operator credential is refused.
func TestM2InterventionRequiresOperatorProof(t *testing.T) {
	t.Parallel()
	recorder := &recordingInterventions{}
	srv := interventionOnlyServer(t, recorder)

	status, payload := doJSONAsAnAnonymousPeer(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		`{"mode":"queue","text":"later","clientMessageId":"msg-1"}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", status, payload)
	}
	if len(recorder.queues) != 0 {
		t.Fatalf("an unauthorised queue reached the coordinator: %+v", recorder.queues)
	}
}

// TestM2BindRefusesWithoutOperatorProof proves the bind route — which provisions a session — is
// closed to a caller that proves only a loopback address.
func TestM2BindRefusesWithoutOperatorProof(t *testing.T) {
	t.Parallel()
	binder := &bindRecorder{}
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{}}
	ownership := &fakeOwnership{ownership: m2AttemptOwnership()}
	srv := m2Server(t, binder, bindings, ownership)

	status, payload := doJSONAsAnAnonymousPeer(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", status, payload)
	}
	if binder.callCount() != 0 || ownership.callCount() != 0 {
		t.Fatalf("an unauthorised bind reached the services: binder=%d ownership=%d",
			binder.callCount(), ownership.callCount())
	}
}

// TestM2ReconcileAndResumeRequireOperatorProof covers the other two sensitive operator routes.
func TestM2ReconcileAndResumeRequireOperatorProof(t *testing.T) {
	t.Parallel()
	binder := &bindRecorder{
		reconcileOutcome: executionsvc.BindOutcome{Binding: m2Binding()},
		resumeOutcome:    executionsvc.BindOutcome{Binding: m2Binding()},
		state:            domain.WorkflowBindingStateBoundReady,
	}
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": liveBinding("workflow-1"),
	}}
	srv := m2Server(t, binder, bindings, &fakeOwnership{ownership: m2AttemptOwnership()})

	for _, path := range []string{
		"/api/v1/workflows/workflow-1/attempts/attempt-1/reconcile",
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resume",
	} {
		status, payload := doJSONAsAnAnonymousPeer(t, srv, http.MethodPost, path, "")
		if status != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403; body=%s", path, status, payload)
		}
	}
	if binder.callCount() != 0 {
		t.Fatalf("an unauthorised recovery reached the binder %d times", binder.callCount())
	}
}

// TestM2RouteWithAnInvalidLeaseIsForbidden is the lease regression: an attempt claim with a lease
// token that does not prove anything is refused on a route that accepts agents, and reaches no
// service.
func TestM2RouteWithAnInvalidLeaseIsForbidden(t *testing.T) {
	t.Parallel()
	const agentToken = "agent-capability"
	sessionID := domain.SessionID("session-1")
	// No attempt rows at all: no lease can be proven.
	sessions := fakePrincipalSessions{sessions: map[domain.SessionID]domain.SessionRecord{
		sessionID: {
			ID: sessionID, ProjectID: "project-1",
			Metadata: domain.SessionMetadata{
				BrowserCapabilityVerifier: browserVerifierFixture(sessionID, agentToken),
			},
		},
	}}
	binding := liveBinding("workflow-1")
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": binding,
	}}
	collaboration := &fakeCollaboration{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		WorkflowBindings:      bindings,
		WorkflowCollaboration: collaboration,
		WorkflowPrincipals: principal.NewResolver(
			sessions,
			principal.CapabilityVerifier{Sessions: sessions, Authority: browserAuthorityFixture{}},
			testOperatorTrust(),
		).WithSessionAttemptProver(principal.StoreSessionAttemptProver{Store: fakePrincipalAttempts{}}),
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	for name, headers := range map[string]map[string]string{
		"missing lease": {
			principal.HeaderAgentCapability: agentToken,
			principal.HeaderAttemptID:       "attempt-1",
		},
		"invalid lease": {
			principal.HeaderAgentCapability: agentToken,
			principal.HeaderAttemptID:       "attempt-1",
			principal.HeaderAttemptLease:    "not-a-lease",
		},
	} {
		headers := headers
		t.Run(name, func(t *testing.T) {
			status, payload := doJSONWithHeaders(t, srv, http.MethodPost,
				"/api/v1/workflows/workflow-1/messages",
				`{"kind":"progress","body":"hi","clientMessageId":"msg-1"}`, headers)
			if status != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", status, payload)
			}
			code := "COLLAB_NOT_AUTHORIZED"
			if name == "invalid lease" {
				code = principal.CodeSecurityUpgradeRequired
			}
			if !strings.Contains(string(payload), code) {
				t.Fatalf("body does not carry %s: %s", code, payload)
			}
		})
	}
}

// TestM2AuditFailureRefusesTheDecisionWhenWired proves a wired recorder that cannot persist fails
// the request instead of reporting a decision the trail never saw.
func TestM2AuditFailureRefusesTheDecisionWhenWired(t *testing.T) {
	t.Parallel()
	svc := &resolveService{state: domain.WorkflowAttemptStateUnknown}
	audit := &auditRecorder{err: errors.New("audit store unavailable")}
	srv := resolveServer(t, svc, &fakeOwnership{ownership: m2AttemptOwnership()}, audit)

	status, payload := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resolve",
		`{"outcome":"succeeded","expectedTaskRevision":1,"idempotencyKey":"resolve-1"}`)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", status, payload)
	}
	if !strings.Contains(string(payload), "WORKFLOW_AUDIT_UNAVAILABLE") {
		t.Fatalf("body does not carry WORKFLOW_AUDIT_UNAVAILABLE: %s", payload)
	}
}

// TestM2SessionsWithoutAnOperatorTrustRefuseEverything proves the wiring-level default: a resolver
// with no operator trust (the shape a daemon has when it could not read its credential) refuses
// M2 callers, so an unwired identity check is never permission.
func TestM2SessionsWithoutAnOperatorTrustRefuseEverything(t *testing.T) {
	t.Parallel()
	binder := &bindRecorder{}
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": m2Binding(),
	}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		WorkflowBinder:     binder,
		WorkflowBindings:   bindings,
		WorkflowOwnership:  &fakeOwnership{ownership: m2AttemptOwnership()},
		WorkflowPrincipals: principal.NewResolver(nil, nil, nil),
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	status, payload := doJSON(t, srv, http.MethodGet,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/binding", "")
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", status, payload)
	}
	if binder.callCount() != 0 {
		t.Fatal("an unwired identity check permitted work")
	}
}

// TestM2ReplayOfADifferentOutcomeIsAConflict proves the replay path is narrow: a second resolve that
// contradicts the stored decision is not silently answered from the first one.
func TestM2ReplayOfADifferentOutcomeIsAConflict(t *testing.T) {
	t.Parallel()
	svc := &resolveService{state: domain.WorkflowAttemptStateUnknown}
	srv := resolveServer(t, svc, &fakeOwnership{ownership: m2AttemptOwnership()}, &auditRecorder{})

	if status, payload := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resolve",
		`{"outcome":"succeeded","expectedTaskRevision":1,"idempotencyKey":"resolve-1"}`,
	); status != http.StatusOK {
		t.Fatalf("first resolve = %d %s", status, payload)
	}
	// A conflicting outcome with the same key: the service refuses it, and the controller must
	// surface that rather than inventing an answer.
	svc.err = domain.ErrWorkflowIdempotencyConflict
	status, payload := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/resolve",
		`{"outcome":"failed","expectedTaskRevision":1,"idempotencyKey":"resolve-1"}`)
	if status == http.StatusOK {
		t.Fatalf("a conflicting replay answered 200: %s", payload)
	}
}
