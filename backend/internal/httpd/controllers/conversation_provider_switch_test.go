package controllers_test

import (
	"bytes"
	"context"
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

func serveConversations(svc controllers.ConversationService, req *http.Request) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	(&controllers.ConversationsController{Svc: svc}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func providerSwitchRouteRequest(t *testing.T, svc controllers.ConversationService, method, path, body string) (int, []byte) {
	t.Helper()
	rec := serveConversations(svc, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
	return rec.Code, rec.Body.Bytes()
}

func TestProviderSwitchRoutePassesTheRequestAndAnswersOK(t *testing.T) {
	svc := &fakeConversationService{}
	status, body := providerSwitchRouteRequest(t, svc, http.MethodPost, "/sessions/p1-1/provider-switch",
		`{"targetHarness":"codex","idempotencyKey":"k1","model":" gpt-5 ","effort":"high","fastMode":true,"ultracode":true,"autoCompactWindow":"1m","permissionMode":"accept-edits"}`)
	if status != http.StatusOK || string(bytes.TrimSpace(body)) != `{"status":"ok"}` {
		t.Fatalf("switch = %d %s, want 200 with an ok body", status, body)
	}
	want := domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessCodex, IdempotencyKey: "k1", Model: "gpt-5", Effort: "high",
		FastMode: true, Ultracode: true, AutoCompactWindow: "1m", PermissionMode: domain.PermissionMode("accept-edits"),
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

func TestProviderSwitchRouteNamesTheModeAnAgentNeeds(t *testing.T) {
	refusal := &domain.ProviderSwitchError{
		Kind: domain.ProviderSwitchRefusalUnsupported, Detail: "pi lacks approvals",
		Cause: &ports.ChatCapabilityError{
			Harness: domain.HarnessPi, Missing: []ports.ChatCapability{ports.ChatCapabilityApprovals},
			AllowedPermissionModes: []ports.PermissionMode{ports.PermissionModeBypassPermissions},
		},
	}
	status, body := providerSwitchRouteRequest(t, &fakeConversationService{switchErr: refusal},
		http.MethodPost, "/sessions/p1-1/provider-switch", `{"targetHarness":"pi"}`)
	var got struct {
		Details struct {
			MissingCapabilities  []string `json:"missingCapabilities"`
			AllowedApprovalModes []string `json:"allowedApprovalModes"`
		} `json:"details"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if status != http.StatusBadRequest ||
		fmt.Sprint(got.Details.MissingCapabilities) != "[approvals]" ||
		fmt.Sprint(got.Details.AllowedApprovalModes) != "[bypass-permissions]" {
		t.Fatalf("refusal = %d %s, want 400 naming approvals and bypass-permissions", status, body)
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

func TestProviderSwitchRouteSendsTheMessageAfterTheSwitch(t *testing.T) {
	route := func(svc *fakeConversationService, body string) *httptest.ResponseRecorder {
		return serveConversations(svc, httptest.NewRequest(http.MethodPost, "/sessions/p1-1/provider-switch", bytes.NewBufferString(body)))
	}
	withMessage := `{"targetHarness":"codex","message":{"text":"Recall the word.","clientMessageId":"m1"}}`

	svc := &fakeConversationService{}
	rec := route(svc, withMessage)
	if rec.Code != http.StatusOK || svc.sent.Text != "Recall the word." || svc.sent.ClientMessageID != "m1" ||
		svc.sent.Origin != domain.MessageOriginHuman {
		t.Fatalf("switch with message = %d %s, sent %+v", rec.Code, rec.Body, svc.sent)
	}
	var got controllers.ProviderSwitchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Message == nil || got.Message.TurnID != "turn-1" {
		t.Fatalf("response = %s, want the send's turn", rec.Body)
	}

	// Attachments go with the switch too, so no other switch can come between
	// the two and take the message.
	svc = &fakeConversationService{}
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR4nGNgAAIAAAUAAXpeqz8AAAAASUVORK5CYII="
	rec = route(svc, `{"targetHarness":"claude-code","message":{"text":"Inspect this image","clientMessageId":"image-1","attachments":[{"mimeType":"image/png","data":"`+png+`"}]}}`)
	if rec.Code != http.StatusOK || svc.switchRequest.TargetHarness != domain.HarnessClaudeCode ||
		len(svc.sent.Content) != 1 || svc.sent.Content[0].Type != "image" || svc.sent.Content[0].Data != png {
		t.Fatalf("switch with an image = %d %s, sent %+v", rec.Code, rec.Body, svc.sent)
	}

	// A send that fails after the switch is flagged, so the client keeps the
	// message as an unsent send rather than an unswitched one.
	svc = &fakeConversationService{sendErr: ports.ErrSessionNotFound}
	if rec = route(svc, withMessage); rec.Code != http.StatusNotFound || rec.Header().Get("X-AO-Provider-Switched") != "1" ||
		rec.Header().Get("Access-Control-Expose-Headers") != "X-AO-Provider-Switched" {
		t.Fatalf("failed send = %d header %q, want 404 marked switched", rec.Code, rec.Header().Get("X-AO-Provider-Switched"))
	}

	// A client that went away during the switch still gets its message sent.
	svc = &fakeConversationService{}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	serveConversations(svc, httptest.NewRequest(http.MethodPost, "/sessions/p1-1/provider-switch", bytes.NewBufferString(withMessage)).WithContext(gone))
	if svc.switchRequest.TargetHarness != domain.HarnessCodex || svc.sent.ClientMessageID != "m1" || svc.sendCtxErr != nil || svc.switchCtxErr != nil {
		t.Fatalf("after the client left: sent %+v with context error %v, want it sent", svc.sent, svc.sendCtxErr)
	}

	// A refused switch sends nothing and is not marked.
	svc = &fakeConversationService{switchErr: &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalBusy, Detail: "busy"}}
	if rec = route(svc, withMessage); rec.Code != http.StatusConflict || rec.Header().Get("X-AO-Provider-Switched") != "" || svc.sent.Text != "" {
		t.Fatalf("refused switch = %d, sent %+v", rec.Code, svc.sent)
	}

	// An empty message refuses the switch before it starts.
	svc = &fakeConversationService{}
	if rec = route(svc, `{"targetHarness":"codex","message":{"text":""}}`); rec.Code != http.StatusBadRequest || svc.switchRequest.TargetHarness != "" {
		t.Fatalf("empty message = %d, switch request %+v", rec.Code, svc.switchRequest)
	}
}

type unavailableChatService struct {
	fakeConversationService
	asked []domain.AgentHarness
}

func (s *unavailableChatService) ChatUnavailable(_ context.Context, harnesses []domain.AgentHarness) map[domain.AgentHarness]string {
	s.asked = harnesses
	return map[domain.AgentHarness]string{domain.HarnessPi: "pi-acp is not installed"}
}

func TestUnavailableChatAgentsRouteListsMissingAdapters(t *testing.T) {
	svc := &unavailableChatService{}
	status, body := providerSwitchRouteRequest(t, svc, http.MethodGet, "/agents/chat/unavailable?agents=codex,%20pi,,", "")
	if status != http.StatusOK || string(bytes.TrimSpace(body)) != `{"agents":[{"agent":"pi","reason":"pi-acp is not installed"}]}` {
		t.Fatalf("unavailable = %d %s", status, body)
	}
	if len(svc.asked) != 2 || svc.asked[0] != domain.HarnessCodex || svc.asked[1] != domain.HarnessPi {
		t.Fatalf("asked = %v, want codex and pi", svc.asked)
	}
}

type permissionModesChatService struct{ fakeConversationService }

func (permissionModesChatService) PermissionModes(_ context.Context, harness domain.AgentHarness) ([]ports.PermissionMode, error) {
	switch harness {
	case domain.HarnessPi:
		return []ports.PermissionMode{ports.PermissionModeBypassPermissions}, nil
	case domain.HarnessKimi:
		return []ports.PermissionMode{ports.PermissionModeDefault}, nil
	}
	return nil, ports.ErrChatUnsupported
}

func TestChatPermissionModesRouteListsEachAgentsModes(t *testing.T) {
	status, body := providerSwitchRouteRequest(t, &permissionModesChatService{}, http.MethodGet,
		"/agents/chat/permission-modes?agents=pi,kimi,unknown", "")
	want := `{"agents":[{"agent":"pi","modes":["bypass-permissions"]},{"agent":"kimi","modes":["default"]}]}`
	if status != http.StatusOK || string(bytes.TrimSpace(body)) != want {
		t.Fatalf("permission modes = %d %s\nwant 200 %s", status, body, want)
	}
}
