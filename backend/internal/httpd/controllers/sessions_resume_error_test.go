package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type resumeErrorCommander struct {
	*sessionmanager.Manager
	err     error
	resumed domain.SessionID
}

func (c *resumeErrorCommander) ResumeAgentWithMode(_ context.Context, id domain.SessionID) (sessionmanager.RestoreResult, error) {
	c.resumed = id
	return sessionmanager.RestoreResult{}, c.err
}

func TestSessionsAPI_ResumeAgentErrorContract(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantKind   string
		wantCode   string
	}{
		{
			name:       "initialize deadline is retryable",
			err:        fmt.Errorf("resume agent ao-1: resume chat: initialize: %w", context.DeadlineExceeded),
			wantStatus: http.StatusServiceUnavailable,
			wantKind:   "unavailable",
			wantCode:   "SERVICE_UNAVAILABLE",
		},
		{
			name:       "incompatible protocol requires user action",
			err:        fmt.Errorf("resume agent ao-1: resume chat: %w: initialize: method not found", ports.ErrChatDriverIncompatible),
			wantStatus: http.StatusConflict,
			wantKind:   "conflict",
			wantCode:   "CHAT_DRIVER_INCOMPATIBLE",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &resumeErrorCommander{err: tc.err}
			service := sessionsvc.NewWithDeps(sessionsvc.Deps{Manager: manager})
			router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.DiscardHandler), nil,
				httpd.APIDeps{Sessions: service}, httpd.ControlDeps{})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/ao-1/resume-agent", nil)
			request.Header.Set("X-Request-ID", "resume-request-1")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != tc.wantStatus {
				t.Fatalf("resume status = %d, want %d; body=%s", response.Code, tc.wantStatus, response.Body)
			}
			var apiError envelope.APIError
			if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
				t.Fatalf("decode resume error: %v", err)
			}
			if apiError.Error != tc.wantKind || apiError.Code != tc.wantCode || apiError.RequestID != "resume-request-1" {
				t.Fatalf("resume error = %+v, want %s/%s with retained requestId", apiError, tc.wantKind, tc.wantCode)
			}
			if manager.resumed != "ao-1" {
				t.Fatalf("resumed session = %q, want ao-1", manager.resumed)
			}
		})
	}
}
