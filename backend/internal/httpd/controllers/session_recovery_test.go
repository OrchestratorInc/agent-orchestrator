package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type normalRecoveryService struct {
	*fakeSessionService
	recoveries int
}

func (s *normalRecoveryService) RestoreWithNormal(ctx context.Context, id domain.SessionID) (sessionsvc.RestoreOutcome, error) {
	s.recoveries++
	return s.Restore(ctx, id)
}
func (s *normalRecoveryService) ResumeAgentWithNormal(ctx context.Context, id domain.SessionID) (sessionsvc.ResumeAgentOutcome, error) {
	s.recoveries++
	return s.ResumeAgent(ctx, id)
}
func TestSessionRecoveryAcceptsOnlyExplicitNormalOverride(t *testing.T) {
	for _, endpoint := range []string{"restore", "resume-agent"} {
		for _, body := range []string{"", `{}`, `{"speedMode":"normal"}`, `{"speedMode":"fast"}`, `{"speedMode":"ultrafast"}`, `{"speedMode":"unknown"}`, `{"speedMode":`} {
			t.Run(endpoint+"/"+body, func(t *testing.T) {
				svc := &normalRecoveryService{fakeSessionService: newFakeSessionService()}
				router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{})
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/ao-1/"+endpoint, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(recorder, request)
				response, status := recorder.Body.String(), recorder.Code
				wantStatus, wantRecoveries := http.StatusOK, 0
				if body == `{"speedMode":"normal"}` {
					wantRecoveries = 1
				} else if body != "" && body != `{}` {
					wantStatus = http.StatusBadRequest
				}
				if status != wantStatus || svc.recoveries != wantRecoveries {
					t.Fatalf("status=%d recoveries=%d body=%s", status, svc.recoveries, response)
				}
			})
		}
	}
}
