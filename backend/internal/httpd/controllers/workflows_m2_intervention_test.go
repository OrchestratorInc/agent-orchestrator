package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	executionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	interventionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// These tests cover the three integration defects: a generation the DTO could not carry, a
// terminated session that surfaced as a 500, and an unadvertised steer that gave the wrong
// advice.

// recordingInterventions is an InterventionCoordinator that records exactly what the handler
// handed it, so a test can prove a field crossed the wire rather than being dropped.
type recordingInterventions struct {
	mu      sync.Mutex
	steers  []interventionsvc.SteerRequest
	queues  []interventionsvc.QueueRequest
	stops   []interventionsvc.StopRequest
	outcome interventionsvc.Outcome
	err     error
	replan  *domain.ReplanRequest
}

func (r *recordingInterventions) Steer(
	_ context.Context,
	request interventionsvc.SteerRequest,
) (interventionsvc.Outcome, error) {
	r.mu.Lock()
	r.steers = append(r.steers, request)
	r.mu.Unlock()
	return r.outcome, r.err
}

func (r *recordingInterventions) Queue(
	_ context.Context,
	request interventionsvc.QueueRequest,
) (interventionsvc.Outcome, error) {
	r.mu.Lock()
	r.queues = append(r.queues, request)
	r.mu.Unlock()
	return r.outcome, r.err
}

func (r *recordingInterventions) Stop(
	_ context.Context,
	request interventionsvc.StopRequest,
) (interventionsvc.Outcome, *domain.ReplanRequest, error) {
	r.mu.Lock()
	r.stops = append(r.stops, request)
	r.mu.Unlock()
	return r.outcome, r.replan, r.err
}

func (r *recordingInterventions) lastSteer() (interventionsvc.SteerRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.steers) == 0 {
		return interventionsvc.SteerRequest{}, false
	}
	return r.steers[len(r.steers)-1], true
}

// interventionOnlyServer wires the intervention slot with a binding for the attempt the tests
// address: the route's cross-workflow guard reads the binding before any service call, so a
// fixture without one would answer 404 and never exercise the DTO.
func interventionOnlyServer(t *testing.T, svc controllers.InterventionCoordinator) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
		"attempt-1": liveBinding("workflow-1"),
	}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		WorkflowBindings:      bindings,
		WorkflowInterventions: svc,
		// The gate's harness comes from durable ownership, never the request body.
		WorkflowOwnership:  &fakeOwnership{ownership: m2AttemptOwnership()},
		WorkflowPrincipals: principal.NewResolver(nil, nil, testOperatorTrust()),
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// TestM2InterventionAcceptsControllerGeneration proves the field is decodable (a strict decoder
// rejects unknown fields, so a missing struct field is a 400, not a silent drop) and that it
// reaches the coordinator on all three modes.
func TestM2InterventionAcceptsControllerGeneration(t *testing.T) {
	t.Parallel()
	recorder := &recordingInterventions{outcome: interventionsvc.Outcome{
		Intervention: domain.WorkflowIntervention{
			ID: "intervention-1", WorkflowID: "workflow-1", TaskID: "task-1",
			Mode: domain.WorkflowInterventionModeQueue, State: domain.WorkflowInterventionStateQueued,
			ClientMessageID: "msg-1", ControllerGeneration: "gen-7", Revision: 1,
		},
		Performed: true,
	}}
	srv := interventionOnlyServer(t, recorder)

	// Steer carries it.
	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		`{"mode":"steer","text":"go left","targetProviderTurnId":"turn-1",`+
			`"clientMessageId":"msg-1","harness":"codex","controllerGeneration":"gen-7"}`)
	if status != http.StatusOK {
		t.Fatalf("steer status = %d, want 200; body=%s", status, body)
	}
	steer, ok := recorder.lastSteer()
	if !ok {
		t.Fatal("the steer never reached the coordinator")
	}
	if steer.ControllerGeneration != "gen-7" {
		t.Fatalf("steer ControllerGeneration = %q, want gen-7", steer.ControllerGeneration)
	}

	// Queue carries it.
	status, body = doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		`{"mode":"queue","text":"later","clientMessageId":"msg-2","controllerGeneration":"gen-7"}`)
	if status != http.StatusOK {
		t.Fatalf("queue status = %d, want 200; body=%s", status, body)
	}
	if len(recorder.queues) != 1 || recorder.queues[0].ControllerGeneration != "gen-7" {
		t.Fatalf("queue ControllerGeneration = %+v, want gen-7", recorder.queues)
	}

	// Stop carries it.
	status, body = doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		`{"mode":"stop","clientMessageId":"msg-3","controllerGeneration":"gen-7"}`)
	if status != http.StatusOK {
		t.Fatalf("stop status = %d, want 200; body=%s", status, body)
	}
	if len(recorder.stops) != 1 || recorder.stops[0].ControllerGeneration != "gen-7" {
		t.Fatalf("stop ControllerGeneration = %+v, want gen-7", recorder.stops)
	}
}

// TestM2StaleGeneratorIsRefusedOverHTTP proves the fence is reachable end to end: the real
// coordinator, a stale generation, and a gateway that must not be steered.
func TestM2StaleGeneratorIsRefusedOverHTTP(t *testing.T) {
	t.Parallel()
	gateway := &countingGateway{
		observation: ports.SessionObservation{
			SessionID: "session-1", Readiness: ports.SessionReadinessReady,
			ControllerGeneration: "gen-1",
		},
	}
	// liveBinding carries ControllerGeneration "gen-1"; the request names "gen-0".
	srv := interventionServer(t, liveBinding("workflow-1"), gateway, newInterventionStore(),
		newStaticOnlyResolver())

	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
		`{"mode":"steer","text":"go left","targetProviderTurnId":"turn-1",`+
			`"clientMessageId":"msg-1","harness":"codex","controllerGeneration":"gen-0"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", status, body)
	}
	if !strings.Contains(string(body), "TURN_MISMATCH") {
		t.Fatalf("body does not carry TURN_MISMATCH: %s", body)
	}
	steers, sends, interrupts := gateway.counts()
	if steers != 0 || sends != 0 || interrupts != 0 {
		t.Fatalf("a stale generation reached the gateway: steer=%d send=%d interrupt=%d",
			steers, sends, interrupts)
	}
}

// TestM2TerminatedSessionIsAConflict proves a terminated session is a typed 409 and not a 500,
// on every route that runs the gateway's session check.
func TestM2TerminatedSessionIsAConflict(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			name:   "terminated",
			err:    ports.ErrExecutionTerminated,
			status: http.StatusConflict,
			code:   "SESSION_TERMINATED",
		},
		{
			name:   "stale generation",
			err:    ports.ErrExecutionStaleGeneration,
			status: http.StatusConflict,
			code:   "TURN_MISMATCH",
		},
		{
			name:   "session mismatch",
			err:    ports.ErrExecutionSessionMismatch,
			status: http.StatusConflict,
			code:   "SESSION_MISMATCH",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			// observe routes its binder error through bindError.
			bindings := &fakeBindings{bindings: map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding{
				"attempt-1": m2Binding(),
			}}
			binder := &bindRecorder{observeErr: testCase.err}
			srv := m2Server(t, binder, bindings, nil)

			status, body := doJSON(t, srv, http.MethodGet,
				"/api/v1/workflows/workflow-1/attempts/attempt-1/observe", "")
			if status != testCase.status {
				t.Fatalf("status = %d, want %d; body=%s", status, testCase.status, body)
			}
			if !strings.Contains(string(body), testCase.code) {
				t.Fatalf("body does not carry %q: %s", testCase.code, body)
			}
			if status == http.StatusInternalServerError {
				t.Fatalf("a classified refusal was reported as an internal error: %s", body)
			}
		})
	}

	// And a bind that hits a terminated session is the same typed refusal.
	binder := &bindRecorder{bindErr: ports.ErrExecutionTerminated}
	srv := m2Server(t, binder, &fakeBindings{}, &fakeOwnership{ownership: m2AttemptOwnership()})
	status, body := doJSON(t, srv, http.MethodPost,
		"/api/v1/workflows/workflow-1/attempts/attempt-1/bind", `{"idempotencyKey":"bind-1"}`)
	if status != http.StatusConflict || !strings.Contains(string(body), "SESSION_TERMINATED") {
		t.Fatalf("terminated bind = %d %s, want 409 SESSION_TERMINATED", status, body)
	}
}

// TestM2UnadvertisedSteerAdvisesTheQueue proves the advice code for a handshake that does not
// advertise steering, instead of falling through to "unknown".
func TestM2UnadvertisedSteerAdvisesTheQueue(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{
		ports.CapabilityReasonNotObserved,
		ports.CapabilityReasonNotAdvertised,
	} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			recorder := &recordingInterventions{outcome: interventionsvc.Outcome{
				ReasonCode: reason, QueueAvailable: true,
			}}
			srv := interventionOnlyServer(t, recorder)

			status, body := doJSON(t, srv, http.MethodPost,
				"/api/v1/workflows/workflow-1/attempts/attempt-1/interventions",
				`{"mode":"steer","text":"go left","targetProviderTurnId":"turn-1",`+
					`"clientMessageId":"msg-1","harness":"codex"}`)
			if status != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body=%s", status, body)
			}
			if !strings.Contains(string(body), "STEER_UNSUPPORTED") {
				t.Fatalf("%s mapped to %s, want STEER_UNSUPPORTED", reason, body)
			}
			if !strings.Contains(string(body), `"queueAvailable":true`) {
				t.Fatalf("the refusal does not offer the queue path: %s", body)
			}
		})
	}

	// The mapping is a pure function, so the full table is pinned directly as well: a reason the
	// daemon cannot classify must keep saying so rather than being rounded into advice.
	for reason, want := range map[string]string{
		ports.CapabilityReasonStaticOnly:         "STEER_UNSUPPORTED",
		ports.CapabilityReasonNotProbed:          "STEER_UNSUPPORTED",
		ports.CapabilityReasonSnapshotExpired:    "STEER_UNSUPPORTED",
		ports.CapabilityReasonProbeFailed:        "STEER_UNSUPPORTED",
		ports.CapabilityReasonProbeTimeout:       "STEER_UNSUPPORTED",
		ports.CapabilityReasonNotObserved:        "STEER_UNSUPPORTED",
		ports.CapabilityReasonNotAdvertised:      "STEER_UNSUPPORTED",
		ports.CapabilityReasonSessionMismatch:    "STEER_UNSUPPORTED",
		ports.CapabilityReasonGenerationMismatch: "TURN_MISMATCH",
		ports.CapabilityReasonNoSession:          "TURN_MISMATCH",
		"session_not_live":                       "SESSION_NOT_READY",
		"binding_not_live":                       "SESSION_NOT_READY",
		"something_the_daemon_has_never_seen":    "CAPABILITY_UNKNOWN",
		"":                                       "INTERVENTION_UNCERTAIN",
	} {
		if got := controllers.InterventionReasonCode(reason); got != want {
			t.Fatalf("InterventionReasonCode(%q) = %q, want %q", reason, got, want)
		}
	}
}

// TestM2SuspendBodyCarriesGenerationInSpec proves the new field is in the published contract:
// a client cannot send what the spec does not describe.
func TestM2SuspendBodyCarriesGenerationInSpec(t *testing.T) {
	t.Parallel()
	srv := interventionOnlyServer(t, &recordingInterventions{})

	resp, err := http.Get(srv.URL + "/api/v1/openapi.yaml")
	if err != nil {
		t.Fatalf("get spec: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	text := string(body)
	start := strings.Index(text, "WorkflowInterventionRequest:")
	if start < 0 {
		t.Fatal("the spec does not describe WorkflowInterventionRequest")
	}
	end := start + 2000
	if end > len(text) {
		end = len(text)
	}
	if !strings.Contains(text[start:end], "controllerGeneration") {
		t.Fatalf("the intervention request schema has no controllerGeneration: %s", text[start:end])
	}

	// And the decoded shape really has it, in case the schema and the Go type drift.
	var decoded controllers.WorkflowInterventionRequest
	if err := json.Unmarshal([]byte(`{"mode":"steer","controllerGeneration":"gen-1"}`), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.ControllerGeneration != "gen-1" {
		t.Fatalf("controllerGeneration = %q", decoded.ControllerGeneration)
	}
}

var _ = executionsvc.BindOutcome{}
