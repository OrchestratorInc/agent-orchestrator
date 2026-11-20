package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Hermes qualifies for the immediate native-identity bind like Codex and
// Claude Code: its restore command resumes the exact stored native
// conversation, and that restore path proves the identity (recorded native
// home + native conversation lookup) before the launch exists. Its fallback
// proof chain is Linux-only, so without the bind a hermes TUI relaunched
// outside a transition (daemon restart + resume) can never re-confirm its
// identity and every later TUI→Chat handoff fails NATIVE_SESSION_UNVERIFIED.
func TestResumeAgent_HermesNativeResumeBindsIdentity(t *testing.T) {
	baseRuntime := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	runtime := &fakeRestartRuntime{fakeRuntime: baseRuntime}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"hermes", "chat", "--cli", "--in", "/ws/mer-1", "--continue", "ao-mer-1-abc"}}}
	m, st, _ := newExitedResumeManager(t, runtime, agent)
	rec := st.sessions["mer-1"]
	rec.Harness = domain.HarnessHermes
	st.sessions["mer-1"] = rec

	result, err := m.ResumeAgentWithMode(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.IsTerminated || got.Activity.State != domain.ActivityIdle {
		t.Fatalf("resumed session = %+v, want live idle", got)
	}
	if got.Metadata.AgentSessionID != "agent-x" {
		t.Fatalf("resumed identity = %q, want agent-x preserved", got.Metadata.AgentSessionID)
	}
	if got.Metadata.AgentSessionIDLaunchID != "launch-new" {
		t.Fatalf("native hermes resume identity launch = %q, want launch-new", got.Metadata.AgentSessionIDLaunchID)
	}
	if result.Mode != RestoreModeNative {
		t.Fatalf("resume mode = %q, want native", result.Mode)
	}
}
