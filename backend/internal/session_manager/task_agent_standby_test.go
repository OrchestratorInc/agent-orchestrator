package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type standbyRecordingLauncher struct {
	*recordingLauncher
	prewarmed []ChatStart
	standbys  []*fakeChatStandby
}

func (l *standbyRecordingLauncher) SupportsChatStandby(domain.AgentHarness) bool { return true }

func (l *standbyRecordingLauncher) PrewarmChatStandby(_ context.Context, cfg ChatStart) (ports.ChatStandby, error) {
	l.prewarmed = append(l.prewarmed, cfg)
	standby := &fakeChatStandby{harness: cfg.Harness}
	l.standbys = append(l.standbys, standby)
	return standby, nil
}

func preparedChatSpawn(t *testing.T) (*Manager, *fakeStore, *standbyRecordingLauncher, domain.TaskPreparationToken) {
	t.Helper()
	launcher := &standbyRecordingLauncher{recordingLauncher: &recordingLauncher{}}
	m, st, _ := newChatManager(launcher)
	m.runBackground = func(work func()) { work() }
	token, err := m.PrepareTaskWorkspace(context.Background(), st.projects["mer"])
	if err != nil {
		t.Fatal(err)
	}
	return m, st, launcher, token
}

func TestNewChatAdoptsAgentStartedForItsTaskPreparation(t *testing.T) {
	m, st, launcher, token := preparedChatSpawn(t)
	if err := m.PrewarmTaskAgent(context.Background(), token, TaskAgentPrewarm{
		Harness: domain.HarnessCodex, Model: "picked-model",
	}); err != nil {
		t.Fatalf("PrewarmTaskAgent: %v", err)
	}
	reserved := st.sessions["mer-1"]
	if len(launcher.prewarmed) != 1 || launcher.prewarmed[0].SessionID != reserved.ID ||
		launcher.prewarmed[0].WorkspacePath != reserved.Metadata.WorkspacePath {
		t.Fatalf("prewarmed = %+v, want the reserved session in its prepared worktree", launcher.prewarmed)
	}

	cfg := asyncChatSpawnConfig("do the thing")
	cfg.Async = false
	cfg.AgentConfig.Model = "picked-model"
	cfg.TaskPreparation = token
	spawned, _, _, err := m.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if spawned.ID != reserved.ID {
		t.Fatalf("spawned %q, want the reserved %q", spawned.ID, reserved.ID)
	}
	if len(launcher.started) != 1 || launcher.started[0].Standby != launcher.standbys[0] {
		t.Fatalf("Chat starts = %+v, want the prepared agent handed to the launch", launcher.started)
	}
}

func TestStandaloneNewChatClaimsItsPreparationAndAgent(t *testing.T) {
	launcher := &standbyRecordingLauncher{recordingLauncher: &recordingLauncher{}}
	m, st, _ := newChatManager(launcher)
	m.runBackground = func(work func()) { work() }
	token, err := m.PrepareStandaloneTaskWorkspace(context.Background())
	if err != nil || token == "" {
		t.Fatalf("PrepareStandaloneTaskWorkspace = %q, %v", token, err)
	}
	reserved := st.sessions[domain.SessionID(token)]
	if !reserved.IsTaskPreparation || reserved.ProjectID != "" || reserved.Metadata.WorkspacePath == "" {
		t.Fatalf("reserved row = %+v, want a hidden standalone row with a workspace", reserved)
	}
	if err := m.PrewarmTaskAgent(context.Background(), token, TaskAgentPrewarm{Harness: domain.HarnessCodex}); err != nil {
		t.Fatalf("PrewarmTaskAgent: %v", err)
	}
	cfg := asyncChatSpawnConfig("do the thing")
	cfg.ProjectID = ""
	cfg.Async = false
	cfg.TaskPreparation = token
	spawned, _, _, err := m.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if spawned.ID != reserved.ID || st.sessions[spawned.ID].IsTaskPreparation {
		t.Fatalf("spawned %+v, want the visible reserved session %q", spawned, reserved.ID)
	}
	if len(launcher.started) != 1 || launcher.started[0].Standby != launcher.standbys[0] {
		t.Fatalf("Chat starts = %+v, want the prepared agent handed to the launch", launcher.started)
	}
}

func TestNewChatWithOtherModelStartsColdAndDropsStandby(t *testing.T) {
	m, _, launcher, token := preparedChatSpawn(t)
	if err := m.PrewarmTaskAgent(context.Background(), token, TaskAgentPrewarm{
		Harness: domain.HarnessCodex, Model: "picked-model",
	}); err != nil {
		t.Fatalf("PrewarmTaskAgent: %v", err)
	}
	cfg := asyncChatSpawnConfig("do the thing")
	cfg.Async = false
	cfg.AgentConfig.Model = "changed-model"
	cfg.TaskPreparation = token
	if _, _, _, err := m.Spawn(context.Background(), cfg); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(launcher.started) != 1 || launcher.started[0].Standby != nil {
		t.Fatalf("Chat starts = %+v, want a cold start", launcher.started)
	}
	if launcher.standbys[0].discarded.Load() == 0 {
		t.Fatal("standby for the other model was not discarded")
	}
}

func TestCancelledTaskPreparationDiscardsItsAgent(t *testing.T) {
	m, _, launcher, token := preparedChatSpawn(t)
	if err := m.PrewarmTaskAgent(context.Background(), token, TaskAgentPrewarm{Harness: domain.HarnessCodex}); err != nil {
		t.Fatalf("PrewarmTaskAgent: %v", err)
	}
	if err := m.CancelTaskPreparation(context.Background(), token); err != nil {
		t.Fatalf("CancelTaskPreparation: %v", err)
	}
	if launcher.standbys[0].discarded.Load() == 0 {
		t.Fatal("agent started for a cancelled preparation was not discarded")
	}
	// A pick after the cancel starts nothing.
	if err := m.PrewarmTaskAgent(context.Background(), token, TaskAgentPrewarm{Harness: domain.HarnessCodex}); err != nil {
		t.Fatalf("PrewarmTaskAgent after cancel: %v", err)
	}
	if len(launcher.prewarmed) != 1 {
		t.Fatalf("prewarmed %d agents, want none after cancellation", len(launcher.prewarmed))
	}
}

func TestRepickedTaskAgentReplacesEarlierStandby(t *testing.T) {
	m, _, launcher, token := preparedChatSpawn(t)
	for _, model := range []string{"first", "second"} {
		if err := m.PrewarmTaskAgent(context.Background(), token, TaskAgentPrewarm{
			Harness: domain.HarnessCodex, Model: model,
		}); err != nil {
			t.Fatalf("PrewarmTaskAgent %s: %v", model, err)
		}
	}
	if len(launcher.standbys) != 2 || launcher.standbys[0].discarded.Load() == 0 ||
		launcher.standbys[1].discarded.Load() != 0 {
		t.Fatal("the second pick did not replace the first standby")
	}
}
