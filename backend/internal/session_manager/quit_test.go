package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestStopAgentsForQuitPreservesResumableSession(t *testing.T) {
	for _, state := range []domain.ActivityState{domain.ActivityIdle, domain.ActivityExited} {
		t.Run(string(state), func(t *testing.T) {
			manager, store, runtime, workspace := newManager()
			store.sessions["mer-1"] = domain.SessionRecord{
				ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
				Activity: domain.Activity{State: state},
				Metadata: domain.SessionMetadata{
					WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1", RuntimeHandleID: "tmux-mer-1",
					RuntimeLaunchID: "launch-current", AgentSessionID: "native-thread-1",
				},
			}
			runtime.aliveByHandle = map[string]bool{"tmux-mer-1": true}
			if err := manager.StopAgentsForQuit(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := store.sessions["mer-1"]
			if runtime.destroyed != 1 || got.IsTerminated || got.Activity.State != domain.ActivityExited {
				t.Fatalf("destroyed=%d, session=%+v", runtime.destroyed, got)
			}
			if got.Metadata.WorkspacePath != "/ws/mer-1" || got.Metadata.Branch != "ao/mer-1" ||
				got.Metadata.AgentSessionID != "native-thread-1" || got.Metadata.RuntimeReleasedLaunchID != "launch-current" {
				t.Fatalf("resumable identity changed: %+v", got.Metadata)
			}
			if workspace.destroyed != 0 {
				t.Fatal("desktop quit removed a worktree")
			}
		})
	}
}

type quitStopLauncher struct {
	recordingLauncher
	store *fakeStore
}

func (l *quitStopLauncher) StopChat(ctx context.Context, id domain.SessionID) error {
	rec := l.store.sessions[id]
	rec.HibernatedAt = nil
	l.store.sessions[id] = rec
	return l.recordingLauncher.StopChat(ctx, id)
}

func TestStopAgentsForQuitLeavesChatsAsleep(t *testing.T) {
	manager, store, _, _ := newManager()
	launcher := &quitStopLauncher{store: store}
	manager.chat = launcher
	asleepSince := time.Unix(100, 0)
	seed := func(id domain.SessionID, kind domain.SessionKind, state domain.ActivityState, hibernatedAt *time.Time) {
		store.sessions[id] = domain.SessionRecord{
			ID: id, ProjectID: "mer", Kind: kind, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
			Activity: domain.Activity{State: state}, HibernatedAt: hibernatedAt,
			Metadata: domain.SessionMetadata{
				WorkspacePath: "/ws/" + string(id), Branch: "ao/" + string(id),
				ProviderConversationID: "native-" + string(id), ControllerGeneration: "gen-1",
			},
		}
	}
	seed("mer-busy", domain.KindWorker, domain.ActivityActive, nil)
	seed("mer-asleep", domain.KindWorker, domain.ActivityIdle, &asleepSince)
	seed("mer-exited", domain.KindWorker, domain.ActivityExited, nil)
	seed("mer-orchestrator", domain.KindOrchestrator, domain.ActivityIdle, nil)

	if err := manager.StopAgentsForQuit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.stopped) != 4 {
		t.Fatalf("quit must still stop every chat provider: %v", launcher.stopped)
	}
	for _, id := range []domain.SessionID{"mer-busy", "mer-asleep"} {
		got := store.sessions[id]
		if got.HibernatedAt == nil || got.Activity.State != domain.ActivityIdle || got.IsTerminated {
			t.Fatalf("%s must be asleep so opening it wakes it: %+v", id, got)
		}
	}
	for _, id := range []domain.SessionID{"mer-exited", "mer-orchestrator"} {
		got := store.sessions[id]
		if got.HibernatedAt != nil || got.Activity.State != domain.ActivityExited || got.IsTerminated {
			t.Fatalf("%s must stay stopped: %+v", id, got)
		}
	}
}
