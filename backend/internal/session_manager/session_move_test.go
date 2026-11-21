package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

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

func (f *fakeStore) SetSessionMoveStarted(_ context.Context, id domain.SessionID, moveID string, _ time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	rec.MoveStarted = moveID
	f.sessions[id] = rec
	return ok, nil
}

// A session whose move to AO Cloud pushed its branch and did not finish may
// be running in AO Cloud: restoring at startup leaves it stopped, and an
// explicit restore (a failed move's rollback, or the user's choice) ends the
// move here.
func TestRestoreAllLeavesAMovingSessionStopped(t *testing.T) {
	m, st, rt, _ := newLifecycleManager()
	for _, id := range []domain.SessionID{"mer-1", "mer-2"} {
		st.sessions[id] = domain.SessionRecord{
			ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, IsTerminated: true,
			Metadata: domain.SessionMetadata{WorkspacePath: "/ws/" + string(id), Branch: "ao/" + string(id), AgentSessionID: "agent-" + string(id)},
			Activity: domain.Activity{State: domain.ActivityExited},
		}
		st.worktrees[id] = []domain.SessionWorktreeRecord{{SessionID: id, RepoName: "__root__", State: "removed"}}
	}
	moving := st.sessions["mer-1"]
	moving.MoveStarted = "m-1"
	st.sessions["mer-1"] = moving

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	if rt.created != 1 || !st.sessions["mer-1"].IsTerminated || st.sessions["mer-2"].IsTerminated {
		t.Fatalf("RestoreAll relaunched %d; mer-1 terminated=%v mer-2 terminated=%v; want only mer-2 relaunched",
			rt.created, st.sessions["mer-1"].IsTerminated, st.sessions["mer-2"].IsTerminated)
	}

	_, _ = m.RestoreWithMode(ctx, "mer-1")
	if got := st.sessions["mer-1"].MoveStarted; got != "" {
		t.Fatalf("explicit restore left MoveStarted = %q", got)
	}
}
