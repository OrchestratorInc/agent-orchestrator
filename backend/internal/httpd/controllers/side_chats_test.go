package controllers_test

import (
	"bytes"
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
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type sideHTTPService struct {
	*chatsvc.Service
	calls   int
	created chatsvc.SideCreateRequest
	sent    ports.ChatUserMessage
}

func (s *sideHTTPService) ListIndependentSideChats(context.Context, domain.SessionID) ([]domain.SideConversation, error) {
	s.calls++
	return []domain.SideConversation{{ID: "side", SessionID: "session", MainConversationID: "main", State: "ready", ProviderHostID: "private-host", ProviderForkID: "private-fork", SourceProviderID: "private-source", AppRunID: "private-launch", NativeAnchorID: "private-anchor", LaunchConfig: json.RawMessage(`{"private":"config"}`)}}, nil
}
func (s *sideHTTPService) CreateIndependentSideChat(_ context.Context, _ domain.SessionID, req chatsvc.SideCreateRequest) (domain.SideConversation, error) {
	s.calls++
	s.created = req
	return domain.SideConversation{ID: "side", State: "opening"}, nil
}
func (s *sideHTTPService) SendSideQuestion(_ context.Context, _ domain.SessionID, _ string, msg ports.ChatUserMessage) (domain.SideTurn, error) {
	s.calls++
	s.sent = msg
	return domain.SideTurn{ID: "turn", Text: msg.Text, State: "queued", ProviderTurnID: "private-turn", Content: []domain.SideContent{{URI: "private-resource", Data: "private-image"}}, References: []domain.SideReference{{ConversationID: "main", MessageID: "source", Selection: "selected", Context: "private-context"}}}, nil
}
func (s *sideHTTPService) SideSnapshot(context.Context, domain.SessionID, string, time.Time, int) (domain.SideSnapshot, error) {
	s.calls++
	return domain.SideSnapshot{}, chatsvc.ErrSideUnavailable
}
func sideHTTPRouter(s *sideHTTPService) http.Handler {
	return httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: newFakeSessionService(), Conversations: s}, httpd.ControlDeps{})
}
func TestSideChatHTTPForwardsReferencesAndKeepsSummariesSafe(t *testing.T) {
	svc := &sideHTTPService{Service: chatsvc.New(chatsvc.Options{})}
	handler := sideHTTPRouter(svc)
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/session/conversation/side-chats", strings.NewReader(`{"idempotencyKey":"create","forceNew":true,"reference":{"conversationId":"main","messageId":"source","revision":3,"text":"selected"}}`)))
	if create.Code != http.StatusCreated || !svc.created.ForceNew || svc.created.Reference == nil || svc.created.Reference.MessageID != "source" {
		t.Fatalf("create: %d %s %+v", create.Code, create.Body.String(), svc.created)
	}
	send := httptest.NewRecorder()
	handler.ServeHTTP(send, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/session/conversation/side-chats/side/messages", strings.NewReader(`{"text":"What is this?","clientMessageId":"receipt","references":[{"conversationId":"main","messageId":"source","revision":3,"text":"selected"}]}`)))
	if send.Code != http.StatusAccepted || svc.sent.Text != "What is this?" || len(svc.sent.Excerpts) != 1 || svc.sent.Excerpts[0].Revision != 3 {
		t.Fatalf("send: %d %s %+v", send.Code, send.Body.String(), svc.sent)
	}
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/session/conversation/side-chats", nil))
	if list.Code != http.StatusOK {
		t.Fatal(list.Code, list.Body.String())
	}
	if bytes.Contains(list.Body.Bytes(), []byte("private")) || bytes.Contains(send.Body.Bytes(), []byte("private")) {
		t.Fatalf("private provider/context escaped: %s %s", list.Body, send.Body)
	}
	if !strings.Contains(send.Body.String(), `"messageId":"source"`) {
		t.Fatal("navigation identity omitted")
	}
}
func TestSideChatHTTPRejectsLANWithoutCallingLocalService(t *testing.T) {
	svc := &sideHTTPService{Service: chatsvc.New(chatsvc.Options{})}
	handler := sideHTTPRouter(svc)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/sessions/session/conversation/side-chats", ""},
		{"POST", "/api/v1/sessions/session/conversation/side-chats", `{"idempotencyKey":"create"}`},
		{"POST", "/api/v1/sessions/session/conversation/side-chats/side/messages", `{"text":"hello","clientMessageId":"receipt"}`},
		{"GET", "/api/v1/side-chats/launch/state?appRunId=launch", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req = req.WithContext(requestscope.WithLAN(req.Context()))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s: %d %s", tc.path, response.Code, response.Body.String())
		}
	}
	if svc.calls != 0 {
		t.Fatal("LAN reached local side service")
	}
}
func TestSideChatHTTPMapsUnavailableOwnershipToConflict(t *testing.T) {
	svc := &sideHTTPService{Service: chatsvc.New(chatsvc.Options{})}
	response := httptest.NewRecorder()
	sideHTTPRouter(svc).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/session/conversation/side-chats/wrong", nil))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "CHAT_SIDE_UNAVAILABLE") {
		t.Fatal(response.Code, response.Body.String())
	}
}
