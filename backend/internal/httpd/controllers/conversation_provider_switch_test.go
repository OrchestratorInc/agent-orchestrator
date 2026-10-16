package controllers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func providerSwitchRouteRequest(t *testing.T, svc controllers.ConversationService, method, path, body string) (int, []byte) {
	t.Helper()
	r := chi.NewRouter()
	(&controllers.ConversationsController{Svc: svc}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
	return rec.Code, rec.Body.Bytes()
}

func TestProviderSwitchRoutePassesTheRequestAndAnswersOK(t *testing.T) {
	svc := &fakeConversationService{}
	status, body := providerSwitchRouteRequest(t, svc, http.MethodPost, "/sessions/p1-1/provider-switch",
		`{"targetHarness":"codex","idempotencyKey":"k1","model":" gpt-5 ","effort":"high","fastMode":true,"ultracode":true,"autoCompactWindow":"1m"}`)
	if status != http.StatusOK || string(bytes.TrimSpace(body)) != `{"status":"ok"}` {
		t.Fatalf("switch = %d %s, want 200 with an ok body", status, body)
	}
	want := domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessCodex, IdempotencyKey: "k1", Model: "gpt-5", Effort: "high",
		FastMode: true, Ultracode: true, AutoCompactWindow: "1m",
	}
	if svc.switchRequest != want {
		t.Fatalf("service request = %+v, want %+v", svc.switchRequest, want)
	}
}

func TestProviderSwitchRouteMapsRefusalsToStatusAndCode(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{&domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalBusy, Detail: "busy"}, http.StatusConflict, "PROVIDER_SWITCH_busy"},
		{&domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalPendingInteraction, Detail: "pending"}, http.StatusConflict, "PROVIDER_SWITCH_pending_interaction"},
		{&domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "recover"}, http.StatusConflict, "PROVIDER_SWITCH_recovery_required"},
		{&domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnsupported, Detail: "no"}, http.StatusBadRequest, "PROVIDER_SWITCH_unsupported"},
		{fmt.Errorf("wrapped: %w", &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnavailable, Detail: "down"}), http.StatusServiceUnavailable, "PROVIDER_SWITCH_unavailable"},
		{ports.ErrSessionNotFound, http.StatusNotFound, "SESSION_NOT_FOUND"},
	} {
		status, body := providerSwitchRouteRequest(t, &fakeConversationService{switchErr: tc.err},
			http.MethodPost, "/sessions/p1-1/provider-switch", `{"targetHarness":"codex"}`)
		var got struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		if status != tc.status || got.Code != tc.code {
			t.Errorf("%v: got %d %s, want %d %s", tc.err, status, got.Code, tc.status, tc.code)
		}
	}
}

func TestSessionHistoryRoutePagesAndValidates(t *testing.T) {
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	svc := &fakeConversationService{historyTotal: 7, history: []domain.HandoffTurn{
		{UserRequest: "ask", AgentResponse: "answer", At: at, Harness: domain.HarnessCodex},
		{UserRequest: "earlier ask", At: at.Add(-time.Hour)},
	}}
	status, body := providerSwitchRouteRequest(t, svc, http.MethodGet, "/sessions/p1-1/history?offset=2&limit=500", "")
	want := `{"turns":[{"userRequest":"ask","agentResponse":"answer","at":"2026-10-11T09:00:00Z","harness":"codex"},` +
		`{"userRequest":"earlier ask","agentResponse":"","at":"2026-10-11T08:00:00Z"}],"total":7,"offset":2}`
	if status != http.StatusOK || string(bytes.TrimSpace(body)) != want {
		t.Fatalf("history = %d %s\nwant 200 %s", status, body, want)
	}
	if svc.historyOffset != 2 || svc.historyLimit != 50 {
		t.Fatalf("service page = offset %d limit %d, want 2 and the 50 cap", svc.historyOffset, svc.historyLimit)
	}

	if _, _ = providerSwitchRouteRequest(t, svc, http.MethodGet, "/sessions/p1-1/history", ""); svc.historyOffset != 0 || svc.historyLimit != 10 {
		t.Fatalf("default page = offset %d limit %d, want 0 and 10", svc.historyOffset, svc.historyLimit)
	}
	for _, query := range []string{"offset=-1", "offset=x", "limit=0", "limit=-3"} {
		if status, body := providerSwitchRouteRequest(t, svc, http.MethodGet, "/sessions/p1-1/history?"+query, ""); status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", query, status, body)
		}
	}
	svc.historyErr = errors.Join(ports.ErrSessionNotFound)
	if status, _ := providerSwitchRouteRequest(t, svc, http.MethodGet, "/sessions/p1-1/history", ""); status != http.StatusNotFound {
		t.Errorf("missing session = %d, want 404", status)
	}
}
