package httpd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func remainingRequestBudget(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return time.Until(deadline)
}

type timeoutProbeSessionService struct {
	controllers.SessionService
	uploadBudget chan time.Duration
	sendBudget   chan time.Duration
}

func (s *timeoutProbeSessionService) StageAttachments(ctx context.Context, _ domain.SessionID, _ []ports.SpawnAttachment) ([]string, error) {
	s.uploadBudget <- remainingRequestBudget(ctx)
	return []string{"attachment.txt"}, nil
}

func (s *timeoutProbeSessionService) Send(ctx context.Context, _ domain.SessionID, _ string, _ *ports.SpawnAttachment) error {
	s.sendBudget <- remainingRequestBudget(ctx)
	return nil
}

func TestAttachmentUploadRouteUsesExtendedTimeout(t *testing.T) {
	svc := &timeoutProbeSessionService{uploadBudget: make(chan time.Duration, 1)}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: 250 * time.Millisecond},
		discardLogger(),
		nil,
		APIDeps{Sessions: svc},
		ControlDeps{},
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/ao-1/attachments", bytes.NewBufferString(`{"attachments":[{"mimeType":"text/plain","data":"YQ=="}]}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST attachments status = %d, want 201; body=%s", response.Code, response.Body.String())
	}
	budget := <-svc.uploadBudget
	if budget < 9*time.Minute || budget > 10*time.Minute {
		t.Fatalf("attachment upload budget = %s, want about 10 minutes", budget)
	}
}

func TestAttachmentCapableSendUsesExtendedTimeoutOnlyForUploads(t *testing.T) {
	const configuredTimeout = 250 * time.Millisecond
	svc := &timeoutProbeSessionService{sendBudget: make(chan time.Duration, 2)}
	router := NewRouterWithControl(
		config.Config{RequestTimeout: configuredTimeout},
		discardLogger(),
		nil,
		APIDeps{Sessions: svc},
		ControlDeps{},
	)
	for _, tc := range []struct {
		name   string
		body   string
		upload bool
	}{
		{name: "plain text", body: `{"message":"hello"}`},
		{name: "attachment", body: `{"message":"hello","attachment":{"mimeType":"text/plain","data":"YQ=="}}`, upload: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/ao-1/send", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.upload {
				req.Header.Set(attachmentUploadHeader, "1")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("send status = %d, want 200; body=%s", response.Code, response.Body.String())
			}
			budget := <-svc.sendBudget
			if tc.upload && (budget < 9*time.Minute || budget > 10*time.Minute) {
				t.Fatalf("attachment budget = %s, want about 10 minutes", budget)
			}
			if !tc.upload && (budget <= 0 || budget > configuredTimeout) {
				t.Fatalf("plain text budget = %s, want no more than %s", budget, configuredTimeout)
			}
		})
	}
}
