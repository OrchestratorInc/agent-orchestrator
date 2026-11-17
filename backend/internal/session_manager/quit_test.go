package sessionmanager

import (
	"context"
	"testing"

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
