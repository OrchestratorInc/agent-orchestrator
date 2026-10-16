package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

type spySink struct {
	ids    []string
	events []string
	props  []map[string]any
}

func (s *spySink) Capture(id, event string, props map[string]any) {
	s.ids, s.events, s.props = append(s.ids, id), append(s.events, event), append(s.props, props)
}

func requestAs(p domain.Principal) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	return r.WithContext(context.WithValue(r.Context(), principalKey, p))
}

func TestCaptureReportsOnlyWorkOSUsers(t *testing.T) {
	spy := &spySink{}
	s := &Server{analytics: spy}

	s.capture(requestAs(domain.Principal{Provider: "workos", ExternalID: "user_01H", Email: "me@example.com"}), "invite.sent", map[string]any{"role": "member"})
	s.capture(requestAs(domain.Principal{Provider: "local", ExternalID: "dev"}), "invite.sent", nil)
	s.capture(requestAs(domain.Principal{Provider: "workos"}), "invite.sent", nil)
	s.capture(httptest.NewRequest(http.MethodPost, "/", nil), "invite.sent", nil)

	if len(spy.events) != 1 || spy.ids[0] != "user_01H" {
		t.Fatalf("captured ids=%v events=%v, want exactly the WorkOS user", spy.ids, spy.events)
	}
	if _, ok := spy.props[0]["email"]; ok {
		t.Fatal("email must not be passed to analytics")
	}
	(&Server{}).capture(requestAs(domain.Principal{Provider: "workos", ExternalID: "u"}), "invite.sent", nil) // nil sink is a no-op
}
