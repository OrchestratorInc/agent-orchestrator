package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func reviewConversationRequest(t *testing.T, client *http.Client, method, url, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

func TestReviewerConversationRoutesDispatchToReviewOwner(t *testing.T) {
	service := &fakeConversationService{}
	server := conversationTestServer(t, service)
	base := server.URL + "/api/v1/reviews/review-1/conversation"

	status, body := reviewConversationRequest(t, server.Client(), http.MethodGet, base+"?beforeSequence=42&limit=25", "")
	if status != http.StatusOK || service.reviewID != "review-1" || service.reviewBefore != 42 || service.reviewLimit != 25 {
		t.Fatalf("snapshot = status %d body %s, call = %q %d %d", status, body, service.reviewID, service.reviewBefore, service.reviewLimit)
	}

	status, body = reviewConversationRequest(t, server.Client(), http.MethodPost, base+"/messages", `{"text":"check this","clientMessageId":"client-1"}`)
	if status != http.StatusAccepted || service.reviewOwner != domain.ReviewConversationOwner("review-1") || service.sent.Text != "check this" {
		t.Fatalf("send = status %d body %s, owner = %#v message = %#v", status, body, service.reviewOwner, service.sent)
	}

	status, body = reviewConversationRequest(t, server.Client(), http.MethodPost, base+"/approvals/acp-request%3Ahost%3A1/resolve", `{"decisionId":"allow-once"}`)
	if status != http.StatusNoContent || service.reviewRequestID != "acp-request:host:1" || service.approvalDecision.ID != "allow-once" {
		t.Fatalf("approval = status %d body %s, request = %q decision = %#v", status, body, service.reviewRequestID, service.approvalDecision)
	}

	status, body = reviewConversationRequest(t, server.Client(), http.MethodPost, base+"/inputs/acp-request%3Ahost%3A2/resolve", `{"action":"accept","content":{"choice":"safe"}}`)
	if status != http.StatusNoContent || service.reviewRequestID != "acp-request:host:2" || service.inputResponse.Action != "accept" || service.inputResponse.Content["choice"] != "safe" {
		t.Fatalf("input = status %d body %s, request = %q response = %#v", status, body, service.reviewRequestID, service.inputResponse)
	}

	status, body = reviewConversationRequest(t, server.Client(), http.MethodPost, base+"/interrupt", "")
	if status != http.StatusNoContent || !service.reviewInterrupted {
		t.Fatalf("interrupt = status %d body %s, interrupted = %t", status, body, service.reviewInterrupted)
	}
}

func TestReviewerConversationRoutesValidateRequests(t *testing.T) {
	service := &fakeConversationService{}
	server := conversationTestServer(t, service)
	base := server.URL + "/api/v1/reviews/review-1/conversation"
	cases := []struct {
		method string
		path   string
		body   string
		code   string
	}{
		{http.MethodGet, "?beforeSequence=0", "", "CONVERSATION_CURSOR_INVALID"},
		{http.MethodGet, "?limit=501", "", "CONVERSATION_LIMIT_INVALID"},
		{http.MethodPost, "/messages", `{}`, "CHAT_MESSAGE_EMPTY"},
		{http.MethodPost, "/approvals/request-1/resolve", `{}`, "CHAT_DECISION_REQUIRED"},
		{http.MethodPost, "/inputs/request-1/resolve", `{"action":"unknown"}`, "CHAT_INPUT_ACTION_INVALID"},
		{http.MethodPost, "/inputs/request-1/resolve", `{"action":"decline","content":{"reason":"no"}}`, "CHAT_INPUT_CONTENT_INVALID"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			status, body := reviewConversationRequest(t, server.Client(), tc.method, base+tc.path, tc.body)
			var apiErr struct {
				Code      string `json:"code"`
				RequestID string `json:"requestId"`
			}
			if err := json.Unmarshal(body, &apiErr); err != nil {
				t.Fatalf("decode error body %s: %v", body, err)
			}
			if status != http.StatusBadRequest || apiErr.Code != tc.code || apiErr.RequestID == "" {
				t.Fatalf("status = %d error = %#v body = %s", status, apiErr, body)
			}
		})
	}
}

func TestReviewerConversationRoutesPreserveServiceErrorEnvelope(t *testing.T) {
	service := &fakeConversationService{reviewErr: errors.New("database unavailable")}
	server := conversationTestServer(t, service)
	base := server.URL + "/api/v1/reviews/review-1/conversation"
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "", ""},
		{http.MethodPost, "/messages", `{"text":"check"}`},
		{http.MethodPost, "/approvals/request-1/resolve", `{"decisionId":"allow-once"}`},
		{http.MethodPost, "/inputs/request-1/resolve", `{"action":"cancel"}`},
		{http.MethodPost, "/interrupt", ""},
	} {
		status, body := reviewConversationRequest(t, server.Client(), tc.method, base+tc.path, tc.body)
		if status != http.StatusInternalServerError || !bytes.Contains(body, []byte(`"code":"INTERNAL_ERROR"`)) || !bytes.Contains(body, []byte(`"requestId":"`)) {
			t.Fatalf("%s %s = status %d body %s", tc.method, tc.path, status, body)
		}
	}
}

// ownerRecorder notes which conversation owner each command reached, so the
// reviewer routes can be shown to share the session handlers rather than a subset.
type ownerRecorder struct {
	*fakeConversationService
	owners map[string]domain.ConversationOwner
}

func (o *ownerRecorder) note(op string, owner domain.ConversationOwner) { o.owners[op] = owner }

func (o *ownerRecorder) ModelsForOwner(_ context.Context, owner domain.ConversationOwner) ([]ports.ChatModel, domain.ConversationSettings, error) {
	o.note("models", owner)
	return nil, domain.ConversationSettings{}, nil
}

func (o *ownerRecorder) SetTurnSettingsForOwner(_ context.Context, owner domain.ConversationOwner, settings domain.ConversationSettings) (domain.ConversationSettings, error) {
	o.note("settings", owner)
	return settings, nil
}

func (o *ownerRecorder) SkillsForOwner(_ context.Context, owner domain.ConversationOwner) ([]ports.ChatSkill, error) {
	o.note("skills", owner)
	return nil, nil
}

func (o *ownerRecorder) CompactForOwner(_ context.Context, owner domain.ConversationOwner) (ports.ChatCompactionResult, error) {
	o.note("compact", owner)
	return ports.ChatCompactionResult{}, nil
}

func (o *ownerRecorder) EditMessageForOwner(_ context.Context, owner domain.ConversationOwner, _ string, _ ports.ChatUserMessage) (chatsvc.EditMessageResult, error) {
	o.note("edit", owner)
	return chatsvc.EditMessageResult{Turn: domain.ConversationTurn{ID: "edited"}}, nil
}

func (o *ownerRecorder) RollbackForOwner(_ context.Context, owner domain.ConversationOwner, _ string) (int, error) {
	o.note("rollback", owner)
	return 1, nil
}

func (o *ownerRecorder) SteerForOwner(_ context.Context, owner domain.ConversationOwner, _ ports.ChatUserMessage) (chatsvc.SteerResult, error) {
	o.note("steer", owner)
	return chatsvc.SteerResult{}, nil
}

func TestReviewerConversationServesEverySessionChatCommand(t *testing.T) {
	for _, tc := range []struct {
		name  string
		base  string
		owner domain.ConversationOwner
	}{
		{"session", "/api/v1/sessions/p1-1/conversation", domain.SessionConversationOwner("p1-1")},
		{"reviewer", "/api/v1/reviews/review-1/conversation", domain.ReviewConversationOwner("review-1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &ownerRecorder{fakeConversationService: &fakeConversationService{}, owners: map[string]domain.ConversationOwner{}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			server := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
				Sessions: newFakeSessionService(), Conversations: service,
			}, httpd.ControlDeps{}))
			t.Cleanup(server.Close)
			base := server.URL + tc.base
			for _, call := range []struct{ op, method, path, body string }{
				{"models", http.MethodGet, "/models", ""},
				{"settings", http.MethodPatch, "/settings", `{"model":"gpt-reviewer"}`},
				{"skills", http.MethodGet, "/skills", ""},
				{"compact", http.MethodPost, "/compact", ""},
				{"edit", http.MethodPost, "/turns/turn-2/edit", `{"text":"edited","clientMessageId":"edit-1"}`},
				{"rollback", http.MethodPost, "/turns/turn-2/rollback", ""},
				{"steer", http.MethodPost, "/steer", `{"text":"also check tests","clientMessageId":"steer-1"}`},
			} {
				status, body := reviewConversationRequest(t, server.Client(), call.method, base+call.path, call.body)
				if status >= 300 {
					t.Fatalf("%s %s = %d %s", call.method, call.path, status, body)
				}
				if got := service.owners[call.op]; got != tc.owner {
					t.Fatalf("%s reached owner %#v, want %#v", call.op, got, tc.owner)
				}
			}
		})
	}
}
