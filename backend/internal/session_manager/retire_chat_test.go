package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// errStopLauncher fails every controller stop, like a provider that is out of
// quota and no longer answers. It writes to log so tests can check ordering.
type errStopLauncher struct {
	recordingLauncher
	log *[]string
}

func (l *errStopLauncher) StopChat(ctx context.Context, id domain.SessionID) error {
	_ = l.recordingLauncher.StopChat(ctx, id)
	if l.log != nil {
		*l.log = append(*l.log, "StopChat:"+string(id))
	}
	return errors.New("provider unresponsive")
}

// hangingStopLauncher never finishes a stop until its context ends.
type hangingStopLauncher struct {
	recordingLauncher
}

func (l *hangingStopLauncher) StopChat(ctx context.Context, id domain.SessionID) error {
	_ = l.recordingLauncher.StopChat(ctx, id)
	<-ctx.Done()
	return ctx.Err()
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

// A Chat orchestrator owns a provider process whose cwd is its worktree. The
// process must be stopped before the worktree is removed, as Kill does. A
// provider that fails to stop must not block the replacement (#6449).
func TestRetireForReplacementStopsChatController(t *testing.T) {
	for _, tc := range []struct {
		name          string
		withWorkspace bool
	}{
		{"with workspace", true},
		{"without workspace", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var log []string
			launcher := &errStopLauncher{log: &log}
			m, st, _ := newChatManager(launcher)
			ws := &fakeWorkspace{sharedLog: &log}
			m.workspace = ws
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
			if !tc.withWorkspace {
				return
			}
			stopIdx, destroyIdx := -1, -1
			for i, call := range log {
				switch call {
				case "StopChat:" + string(rec.ID):
					stopIdx = i
				case "ForceDestroy:" + string(rec.ID):
					destroyIdx = i
				}
			}
			if stopIdx == -1 || destroyIdx == -1 || stopIdx > destroyIdx {
				t.Fatalf("the controller must stop before the worktree is removed; log=%v", log)
			}
		})
	}
}

// A stop that never returns is bounded, so the replacement still finishes.
func TestRetireForReplacementBoundsAHangingChatStop(t *testing.T) {
	prev := chatRetireStopBudget
	chatRetireStopBudget = 50 * time.Millisecond
	t.Cleanup(func() { chatRetireStopBudget = prev })

	launcher := &hangingStopLauncher{}
	m, st, _ := newChatManager(launcher)
	rec := chatOrchestratorRecord(true)
	st.sessions[rec.ID] = rec

	done := make(chan error, 1)
	go func() { done <- m.RetireForReplacement(context.Background(), rec.ID) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RetireForReplacement err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retirement waited on a controller stop that never returns")
	}
	if !st.sessions[rec.ID].IsTerminated {
		t.Fatal("retired orchestrator must be marked terminated")
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
