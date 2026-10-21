package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

type terminatedSessionStore struct {
	workspaceHandlerStore
}

func (s *terminatedSessionStore) ResumeSession(context.Context, domain.Principal, string, string) (domain.SandboxLifecycle, error) {
	return domain.SandboxLifecycle{}, postgres.ErrSessionTerminated
}

func TestResumeOfDeletedSessionAsksForRestore(t *testing.T) {
	for name, call := range map[string]func(*Server, http.ResponseWriter, *http.Request){
		"resume":         (*Server).resumeSession,
		"workspace read": (*Server).getWorkspaceDiff,
	} {
		t.Run(name, func(t *testing.T) {
			store := &terminatedSessionStore{}
			server := workspaceHandlerServer(store)
			recorder := httptest.NewRecorder()
			call(server, recorder, workspaceHandlerRequest(t, ""))
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
			var body errorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "SESSION_TERMINATED" || body.Message != "This session was deleted. Restore it to continue." {
				t.Fatalf("error = %q %q", body.Code, body.Message)
			}
			if store.created {
				t.Fatal("a worker request was queued for a deleted session")
			}
		})
	}
}
