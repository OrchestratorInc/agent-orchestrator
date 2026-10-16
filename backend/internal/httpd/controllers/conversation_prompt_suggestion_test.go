package controllers_test

import (
	"context"
	"errors"
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
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type promptSuggestionStub struct {
	*fakeChatService
	prompt  string
	err     error
	called  bool
	session domain.SessionID
	turnID  string
}

func (s *promptSuggestionStub) PromptSuggestion(_ context.Context, id domain.SessionID, turnID string) (string, error) {
	s.called, s.session, s.turnID = true, id, turnID
	return s.prompt, s.err
}

func promptSuggestionServer(t *testing.T, svc controllers.ConversationService) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{Conversations: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPromptSuggestionRouteReturnsTurnAndOptionalPrompt(t *testing.T) {
	for _, prompt := range []string{"Add regression coverage.", ""} {
		svc := &promptSuggestionStub{fakeChatService: &fakeChatService{}, prompt: prompt}
		srv := promptSuggestionServer(t, svc)
		body, status, headers := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/conversation/prompt-suggestion", `{"turnId":"turn-7"}`)
		assertJSON(t, headers)
		var response controllers.ConversationPromptSuggestionResponse
		mustJSON(t, body, &response)
		if status != http.StatusOK || response.TurnID != "turn-7" || response.Prompt != prompt || svc.session != "ao-1" || svc.turnID != "turn-7" {
			t.Fatalf("suggestion route: status=%d response=%+v session=%s turn=%s", status, response, svc.session, svc.turnID)
		}
	}
}

func TestPromptSuggestionRouteRejectsInvalidTurnIDBeforeProviderCall(t *testing.T) {
	for _, body := range []string{`{}`, `{"turnId":""}`, `{"turnId":" turn-7 "}`, `{"turnId":3}`, `{"turnId":"` + strings.Repeat("x", 129) + `"}`, `broken`} {
		svc := &promptSuggestionStub{fakeChatService: &fakeChatService{}}
		srv := promptSuggestionServer(t, svc)
		response, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/conversation/prompt-suggestion", body)
		if status != http.StatusBadRequest || svc.called {
			t.Fatalf("body %q: status=%d called=%v response=%s", body, status, svc.called, response)
		}
	}
}

func TestPromptSuggestionRoutePreservesTypedErrorEnvelopes(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{
		{ports.ErrSessionNotFound, http.StatusNotFound, "SESSION_NOT_FOUND"},
		{chatsvc.ErrNotChatMode, http.StatusConflict, "SESSION_MODE_MISMATCH"},
		{chatsvc.ErrPromptSuggestionStale, http.StatusConflict, "CHAT_PROMPT_SUGGESTION_STALE"},
		{chatsvc.ErrPromptSuggestionBusy, http.StatusTooManyRequests, "CHAT_PROMPT_SUGGESTION_BUSY"},
		{errors.New("provider failed"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	} {
		svc := &promptSuggestionStub{fakeChatService: &fakeChatService{}, err: tt.err}
		srv := promptSuggestionServer(t, svc)
		body, status, headers := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/conversation/prompt-suggestion", `{"turnId":"turn-7"}`)
		if status != tt.status || !strings.Contains(string(body), `"code":"`+tt.code+`"`) {
			t.Fatalf("error %v: status=%d response=%s", tt.err, status, body)
		}
		if errors.Is(tt.err, chatsvc.ErrPromptSuggestionBusy) && headers.Get("Retry-After") != "1" {
			t.Fatalf("busy suggestion has no retry hint: %v", headers)
		}
	}
}

func TestPromptSuggestionRouteDoesNotReportCanceledRequestReads(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		err := context.Canceled
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			err = context.DeadlineExceeded
		} else {
			cancel()
		}
		svc := &promptSuggestionStub{fakeChatService: &fakeChatService{}, err: err}
		router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
			httpd.APIDeps{Conversations: svc}, httpd.ControlDeps{})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/ao-1/conversation/prompt-suggestion", strings.NewReader(`{"turnId":"turn-7"}`)).WithContext(ctx)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		cancel()
		if !svc.called || response.Body.Len() != 0 {
			t.Fatalf("canceled request emitted an error envelope: called=%v response=%s", svc.called, response.Body)
		}
	}
}

func TestPromptSuggestionRouteIsOptional(t *testing.T) {
	srv := promptSuggestionServer(t, &fakeChatService{})
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/ao-1/conversation/prompt-suggestion", `{"turnId":"turn-7"}`)
	if status != http.StatusNotImplemented {
		t.Fatalf("unsupported suggestion: status=%d response=%s", status, body)
	}
}
