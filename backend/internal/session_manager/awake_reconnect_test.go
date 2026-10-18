package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAwakeReconnectOnlyUsesExistingChatProvider(t *testing.T) {
	m, st, rt, ws := newLifecycleManager()
	launcher := &healthCheckLauncher{err: ports.ErrChatHostNotRunning}
	m.chat = launcher
	rec := domain.SessionRecord{ID: "mer-chat", ProjectID: "mer", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Metadata: domain.SessionMetadata{WorkspacePath: "/ws/chat", Branch: "ao/chat/root", ProviderConversationID: "thread", ControllerGeneration: "generation"}, Activity: domain.Activity{State: domain.ActivityActive}}
	st.sessions[rec.ID] = rec
	err := m.ReconnectAwakeAgent(context.Background(), domain.AwakeAgentTarget{SessionID: rec.ID, Owner: rec.ControllerOwner()})
	if !errors.Is(err, ports.ErrChatHostNotRunning) {
		t.Fatalf("error=%v", err)
	}
	if len(launcher.checks) != 1 || !launcher.checks[0].ReconnectOnly || launcher.checks[0].AwakeReconnectTarget == nil || launcher.checks[0].FreshIfProviderConversationMissing {
		t.Fatalf("unsafe launch config: %+v", launcher.checks)
	}
	if rt.created != 0 || len(ws.restoreConfigs) != 0 || st.sessions[rec.ID].Activity.State != domain.ActivityActive {
		t.Fatal("missing provider launched resources or fabricated termination")
	}
}
func TestAwakeReconnectRejectsChangedStoppedOrHibernatedOwners(t *testing.T) {
	for _, kind := range []string{"stale", "terminated", "hibernated", "tui"} {
		t.Run(kind, func(t *testing.T) {
			m, st, rt, ws := newLifecycleManager()
			launcher := &healthCheckLauncher{}
			m.chat = launcher
			rec := domain.SessionRecord{ID: "mer-chat", ProjectID: "mer", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Metadata: domain.SessionMetadata{WorkspacePath: "/ws/chat", Branch: "ao/chat/root", ProviderConversationID: "thread", ControllerGeneration: "generation"}}
			target := domain.AwakeAgentTarget{SessionID: rec.ID, Owner: rec.ControllerOwner()}
			switch kind {
			case "stale":
				rec.Metadata.ControllerGeneration = "replacement"
			case "terminated":
				rec.IsTerminated = true
			case "hibernated":
				now := time.Now()
				rec.HibernatedAt = &now
			case "tui":
				rec.Mode = domain.SessionModeTUI
			}
			st.sessions[rec.ID] = rec
			if err := m.ReconnectAwakeAgent(context.Background(), target); err == nil {
				t.Fatal("ineligible owner accepted")
			}
			if len(launcher.checks) != 0 || rt.created != 0 || len(ws.restoreConfigs) != 0 {
				t.Fatal("guard failure caused launch side effect")
			}
		})
	}
}
