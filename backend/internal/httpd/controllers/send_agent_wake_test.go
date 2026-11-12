package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// wakingSessionService reports whether the send had to restart the agent.
type wakingSessionService struct {
	*fakeSessionService
	result  sessionmanager.SendResult
	err     error
	options ports.MessageDeliveryOptions
	calls   int
}

func (w *wakingSessionService) SendWithResult(_ context.Context, _ domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) (sessionmanager.SendResult, error) {
	w.calls++
	w.sent = message
	w.sentAttachment = attachment
	w.options = options
	return w.result, w.err
}

func newWakingSendServer(t *testing.T, svc *wakingSessionService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, log, nil,
		httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// Every /send caller (the user, `ao send` from a worker or the orchestrator)
// may restart an exited agent, and the answer says whether it did.
func TestSessionsAPI_SendRestartsExitedAgentAndReportsIt(t *testing.T) {
	for _, body := range []string{
		`{"message":"PR is up","senderSessionId":"worker-67"}`,
		`{"message":"hello","userAuthored":true}`,
		`{"message":"hello"}`,
	} {
		svc := &wakingSessionService{fakeSessionService: newFakeSessionService(),
			result: sessionmanager.SendResult{AgentWake: sessionmanager.AgentWakeResumed}}
		srv := newWakingSendServer(t, svc)

		raw, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/orch-48/send", body)
		if status != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", body, status, raw)
		}
		if svc.calls != 1 || !svc.options.WakeExitedAgent {
			t.Fatalf("%s: calls = %d, options = %+v, want one send with the wake option", body, svc.calls, svc.options)
		}
		var resp struct {
			OK        bool   `json:"ok"`
			AgentWake string `json:"agentWake"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatal(err)
		}
		if !resp.OK || resp.AgentWake != "resumed" {
			t.Fatalf("%s: response = %s, want agentWake=resumed", body, raw)
		}
	}
}

func TestSessionsAPI_SendToRunningAgentOmitsAgentWake(t *testing.T) {
	svc := &wakingSessionService{fakeSessionService: newFakeSessionService()}
	srv := newWakingSendServer(t, svc)
	raw, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/send", `{"message":"hi"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, raw)
	}
	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if _, present := resp["agentWake"]; present {
		t.Fatalf("response = %s, want no agentWake for a running agent", raw)
	}
}

func TestSessionsAPI_SendSurfacesWakeFailure(t *testing.T) {
	svc := &wakingSessionService{fakeSessionService: newFakeSessionService(),
		err: apierr.Conflict("AGENT_WAKE_FAILED", "The agent had exited and Orchestrator could not restart it: harness missing.",
			map[string]any{"queued": true})}
	srv := newWakingSendServer(t, svc)
	raw, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/orch-48/send", `{"message":"hi","senderSessionId":"worker-67"}`)
	assertErrorCode(t, raw, status, http.StatusConflict, "AGENT_WAKE_FAILED")
	if svc.calls != 1 {
		t.Fatalf("calls = %d, want 1", svc.calls)
	}
}
