package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reaper"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// A terminal orchestrator whose host died while AO was down (a reboot, a
// crash, an app quit that stopped its agents) must come back stopped and
// resumable. The daemon runs a reaper tick right after the startup check; that
// tick must not read the startup-found death as a crash and archive the
// orchestrator, which removes it from the project.
func TestStartupHealthKeepsDeadTerminalOrchestratorThroughTheReaper(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	now := time.Now().UTC().Truncate(time.Millisecond)
	idleSince := now.Add(-time.Hour)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/repo", Config: testRoleAgents(), RegisteredAt: now}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := st.CreateSession(ctx, domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity:      domain.Activity{State: domain.ActivityIdle, LastActivityAt: idleSince},
		FirstSignalAt: idleSince, CreatedAt: idleSince, UpdatedAt: idleSince,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/orchestrator", Branch: "ao/mer-orchestrator",
			RuntimeHandleID: "ptyhost-v1:mer-1", RuntimeLaunchID: "launch-before-restart",
			AgentSessionID: "agent-x",
		},
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	runtime := &fakeRuntime{} // every handle reads dead, as after a reboot
	lcm := lifecycle.New(st, nil)
	m := New(Deps{
		Runtime:   runtime,
		Agents:    singleAgent{agent: launchArgvAgent{argv: []string{"claude"}}},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: lcm,
		DataDir:   t.TempDir(),
	})

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// The daemon's post-reconcile runtime pass.
	if err := reaper.New(lcm, st, runtime, reaper.Config{}).Tick(ctx); err != nil {
		t.Fatalf("reaper tick: %v", err)
	}
	rec, ok, err := st.GetSession(ctx, "mer-1")
	if err != nil || !ok {
		t.Fatalf("read session: ok=%v err=%v", ok, err)
	}
	if rec.IsTerminated || rec.Activity.State != domain.ActivityExited || rec.Metadata.RuntimeReleasedLaunchID != "launch-before-restart" {
		t.Fatalf("orchestrator after startup = terminated:%v activity:%s released:%q; want an exited, released, live session",
			rec.IsTerminated, rec.Activity.State, rec.Metadata.RuntimeReleasedLaunchID)
	}
	if runtime.created != 0 {
		t.Fatalf("startup launched %d runtimes; it must leave the agent stopped", runtime.created)
	}
}
