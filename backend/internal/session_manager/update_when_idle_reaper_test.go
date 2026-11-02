package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reaper"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// idleRestartRuntime restarts a terminal in place and reports no command
// running in it.
type idleRestartRuntime struct{ *fakeRestartRuntime }

func (idleRestartRuntime) RunningWorkloadCommands(context.Context, ports.RuntimeHandle, ports.SupervisedProcessRef) (int, error) {
	return 0, nil
}

// An update stops an idle terminal and installs while it is down. The reaper
// ticks meanwhile and finds the dead runtime of a session idle for longer than
// the recent-activity window: it must leave the session the update resumes.
func TestTerminalStoppedForUpdateSurvivesTheReaperAndResumes(t *testing.T) {
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
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity:      domain.Activity{State: domain.ActivityIdle, LastActivityAt: idleSince},
		FirstSignalAt: idleSince, CreatedAt: idleSince, UpdatedAt: idleSince,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1",
			RuntimeHandleID: "tmux-mer-1", RuntimeLaunchID: "launch-old",
			AgentSessionID: "agent-x", Prompt: "continue the task",
		},
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	base := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	runtime := idleRestartRuntime{&fakeRestartRuntime{fakeRuntime: base}}
	lcm := lifecycle.New(st, nil)
	m := New(Deps{
		Runtime:     runtime,
		Agents:      singleAgent{agent: supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}},
		Workspace:   &fakeWorkspace{},
		Store:       st,
		Messenger:   &fakeMessenger{},
		Lifecycle:   lcm,
		DataDir:     t.TempDir(),
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		Executable:  func() (string, error) { return "/opt/ao", nil },
		NewLaunchID: func() string { return "launch-new" },
	})

	stopped, err := m.StopHarnessSessions(ctx, domain.HarnessCodex)
	if err != nil || len(stopped) != 1 || stopped[0] != "mer-1" {
		t.Fatalf("stop = %v, %v; want mer-1 stopped", stopped, err)
	}
	if alive, _ := base.IsAlive(ctx, ports.RuntimeHandle{ID: "tmux-mer-1"}); alive {
		t.Fatal("the terminal still runs after the stop")
	}

	// The installer runs now, and the reaper ticks.
	if err := reaper.New(lcm, st, runtime, reaper.Config{}).Tick(ctx); err != nil {
		t.Fatalf("reaper tick: %v", err)
	}
	rec, ok, err := st.GetSession(ctx, "mer-1")
	if err != nil || !ok {
		t.Fatalf("read session: ok=%v err=%v", ok, err)
	}
	if rec.IsTerminated || rec.Activity.State != domain.ActivityExited || rec.Metadata.RuntimeReleasedLaunchID != "launch-old" {
		t.Fatalf("session after a reaper tick = terminated:%v activity:%s released:%q; want an exited, released, live session",
			rec.IsTerminated, rec.Activity.State, rec.Metadata.RuntimeReleasedLaunchID)
	}

	if err := m.ResumeHarnessSessions(ctx, stopped); err != nil {
		t.Fatalf("resume after update: %v", err)
	}
	rec, _, err = st.GetSession(ctx, "mer-1")
	// The stop destroyed the runtime, so resume creates a new one rather than
	// restarting the old handle.
	if err != nil || rec.IsTerminated || rec.Metadata.RuntimeLaunchID != "launch-new" || base.created != 1 || runtime.restarted != 0 {
		t.Fatalf("resumed session = terminated:%v launch:%q created:%d restarts:%d (%v); want the terminal recreated on a new launch",
			rec.IsTerminated, rec.Metadata.RuntimeLaunchID, base.created, runtime.restarted, err)
	}
}
