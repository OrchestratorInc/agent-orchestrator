package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// errStopLauncher fails every controller stop, like a provider that is out of
// quota and no longer answers.
type errStopLauncher struct {
	recordingLauncher
}

func (l *errStopLauncher) StopChat(ctx context.Context, id domain.SessionID) error {
	_ = l.recordingLauncher.StopChat(ctx, id)
	return errors.New("provider unresponsive")
}

func chatOrchestratorRecord(withWorkspace bool) domain.SessionRecord {
	rec := domain.SessionRecord{
		ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Mode: domain.SessionModeChat, Harness: domain.HarnessCodex,
	}
	if withWorkspace {
		rec.Metadata.WorkspacePath = "/ws/mer-orch"
		rec.Metadata.Branch = "ao/mer-orchestrator"
	}
	return rec
}

// A Chat orchestrator owns a provider process that must be stopped before its
// worktree is removed, as Kill does. A provider that fails to stop (usage limit,
// internal error) must not block the replacement (#6449).
func TestRetireForReplacementStopsChatController(t *testing.T) {
	for _, tc := range []struct {
		name          string
		withWorkspace bool
	}{
		{"with workspace", true},
		{"without workspace", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launcher := &errStopLauncher{}
			m, st, _ := newChatManager(launcher)
			rec := chatOrchestratorRecord(tc.withWorkspace)
			st.sessions[rec.ID] = rec

			if err := m.RetireForReplacement(context.Background(), rec.ID); err != nil {
				t.Fatalf("RetireForReplacement err = %v, want nil despite a failing controller stop", err)
			}
			if len(launcher.stopped) != 1 || launcher.stopped[0] != rec.ID {
				t.Fatalf("stopped controllers = %v, want [%s]", launcher.stopped, rec.ID)
			}
			if !st.sessions[rec.ID].IsTerminated {
				t.Fatal("retired orchestrator must be marked terminated")
			}
		})
	}
}

func TestRetireForReplacementDoesNotStopChatForTUISession(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	rec := chatOrchestratorRecord(true)
	rec.Mode = domain.SessionModeTUI
	st.sessions[rec.ID] = rec

	if err := m.RetireForReplacement(context.Background(), rec.ID); err != nil {
		t.Fatalf("RetireForReplacement err = %v", err)
	}
	if len(launcher.stopped) != 0 {
		t.Fatalf("stopped controllers = %v, want none for a TUI session", launcher.stopped)
	}
}
