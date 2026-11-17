package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestSessionLegacyInterruptStillRefusesTUI(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		sessions := &fakeSessionLifecycle{}
		svc := sessionConversationService{
			Service:  chatsvc.New(chatsvc.Options{Sessions: reportDeliveryStore{rec: domain.SessionRecord{ID: "ao-1", Mode: domain.SessionModeTUI}}}),
			sessions: sessions,
		}
		router := chi.NewRouter()
		controller := controllers.ConversationsController{Svc: svc}
		controller.Register(router)
		path := "/sessions/ao-1/stop-turn"
		if legacy {
			path = "/sessions/ao-1/conversation/interrupt"
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if legacy {
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "SESSION_MODE_MISMATCH") || sessions.interrupted != "" {
				t.Fatalf("legacy interrupt: status=%d body=%s terminal=%q", response.Code, response.Body, sessions.interrupted)
			}
		} else if response.Code != http.StatusNoContent || sessions.interrupted != "ao-1" {
			t.Fatalf("stop turn: status=%d terminal=%q", response.Code, sessions.interrupted)
		}
	}
}
