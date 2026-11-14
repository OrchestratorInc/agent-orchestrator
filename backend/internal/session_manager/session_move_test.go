package sessionmanager

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestMovedSessionCannotRunHereAgain(t *testing.T) {
	m, st, _, _ := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	rec := st.sessions["mer-1"]
	rec.MovedTo = &domain.SessionMove{MoveID: "m-1", OrgID: "org-1", SessionID: "cloud-2"}
	st.sessions["mer-1"] = rec
	if _, err := m.RestoreWithMode(ctx, "mer-1"); !errors.Is(err, ErrSessionMoved) {
		t.Fatalf("restore err = %v, want ErrSessionMoved", err)
	}
	if _, err := m.ResumeAgentWithMode(ctx, "mer-1"); !errors.Is(err, ErrSessionMoved) {
		t.Fatalf("resume err = %v, want ErrSessionMoved", err)
	}
}
