package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func seedExitedChatSession(st *fakeStore, id domain.SessionID) {
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: chatTestProject, Kind: domain.KindWorker,
		Harness: domain.HarnessAgy, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/tmp/mer-1", Branch: "ao/mer-1",
			ProviderConversationID: "agy-native",
		},
	}
}

// A switch away from an agent that cannot start launches the target with the
// target's own launch, never the exited source's, and drops the source's
// provider conversation.
func TestLaunchColdChatSwitchBuildsTheTargetLaunch(t *testing.T) {
	m, st, _ := newChatManager(&recordingLauncher{})
	seedExitedChatSession(st, "mer-1")
	var got ChatStart
	err := m.LaunchColdChatSwitch(context.Background(), "mer-1", domain.HarnessClaudeCode,
		func(_ context.Context, cfg ChatStart) error {
			got = cfg
			return nil
		})
	if err != nil {
		t.Fatalf("LaunchColdChatSwitch: %v", err)
	}
	if got.Harness != domain.HarnessClaudeCode || got.SessionID != "mer-1" || got.WorkspacePath != "/tmp/mer-1" {
		t.Fatalf("launch = harness %s session %s workspace %s, want the target in the session's worktree",
			got.Harness, got.SessionID, got.WorkspacePath)
	}
	if got.ProviderConversationID != "" {
		t.Fatalf("launch resumes %q; the target starts a fresh thread", got.ProviderConversationID)
	}
	if got.PrepareControllerEnv == nil {
		t.Fatal("launch has no controller env preparation")
	}
	m.agentOpMu.Lock()
	_, held := m.agentOperations["mer-1"]
	m.agentOpMu.Unlock()
	if held {
		t.Fatal("agent operation still held after the switch returned")
	}
}

// A resume the user started holds the session; the cold switch must not start a
// second agent beside it.
func TestLaunchColdChatSwitchRefusesWhileAResumeRuns(t *testing.T) {
	m, st, _ := newChatManager(&recordingLauncher{})
	seedExitedChatSession(st, "mer-1")
	m.agentOpMu.Lock()
	m.agentOperations["mer-1"] = agentOperationResume
	m.agentOpMu.Unlock()
	started := false
	err := m.LaunchColdChatSwitch(context.Background(), "mer-1", domain.HarnessClaudeCode,
		func(context.Context, ChatStart) error {
			started = true
			return nil
		})
	var refusal *domain.ProviderSwitchError
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalBusy {
		t.Fatalf("err = %v, want a busy refusal", err)
	}
	if started {
		t.Fatal("target started while a resume held the session")
	}
}
