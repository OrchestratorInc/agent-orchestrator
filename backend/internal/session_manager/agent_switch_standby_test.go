package sessionmanager

import (
	"context"
	"maps"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeChatStandby struct {
	harness   domain.AgentHarness
	discarded atomic.Int32
}

func (s *fakeChatStandby) Harness() domain.AgentHarness { return s.harness }
func (s *fakeChatStandby) Discard()                     { s.discarded.Add(1) }

type standbyChatLauncher struct {
	*switchAgentChatLauncher
	prewarmed []ChatStart
	standbys  []*fakeChatStandby
	// prewarmEntered and prewarmRelease, when set,, hold a prewarm inside the provider start.
	prewarmEntered chan struct{}
	prewarmRelease chan struct{}
}

func (l *standbyChatLauncher) SupportsChatStandby(domain.AgentHarness) bool { return true }

func (l *standbyChatLauncher) PrewarmChatStandby(_ context.Context, cfg ChatStart) (ports.ChatStandby, error) {
	if l.prewarmRelease != nil {
		close(l.prewarmEntered)
		<-l.prewarmRelease
	}
	l.prewarmed = append(l.prewarmed, cfg)
	standby := &fakeChatStandby{harness: cfg.Harness}
	l.standbys = append(l.standbys, standby)
	return standby, nil
}

func newStandbySwitchFixture(t *testing.T) (*Manager, *standbyChatLauncher, domain.SessionID) {
	t.Helper()
	manager, store, _ := newSwitchTestManager(t, &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}})
	rec := store.sessions["proj-1"]
	rec.Mode = domain.SessionModeChat
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now().UTC()}
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	rec.Metadata.AgentSessionID = ""
	rec.Metadata.ProviderConversationID = "source-chat-native"
	rec.Metadata.ControllerGeneration = "source-chat-generation"
	store.sessions[rec.ID] = rec
	launcher := &standbyChatLauncher{switchAgentChatLauncher: &switchAgentChatLauncher{
		recordingLauncher: &recordingLauncher{},
		store:             store,
		live:              true,
	}}
	manager.chat = launcher
	return manager, launcher, rec.ID
}

func TestSwitchAdoptsStandbyStartedOnPick(t *testing.T) {
	manager, launcher, id := newStandbySwitchFixture(t)
	pick := SwitchAgentConfig{TargetHarness: domain.HarnessCodex, Model: "picked-model"}
	if err := manager.PrewarmChatSwitch(context.Background(), id, pick); err != nil {
		t.Fatalf("PrewarmChatSwitch: %v", err)
	}
	if len(launcher.prewarmed) != 1 || launcher.prewarmed[0].Model != "picked-model" ||
		launcher.prewarmed[0].ProviderConversationID != "" {
		t.Fatalf("prewarmed = %+v, want one fresh codex standby with the picked model", launcher.prewarmed)
	}

	pick.IdempotencyKey = "send-with-switch"
	if _, err := switchAgentSynchronously(context.Background(), manager, id, pick); err != nil {
		t.Fatalf("SwitchAgent: %v", err)
	}
	if len(launcher.started) != 1 || launcher.started[0].Standby != launcher.standbys[0] {
		t.Fatalf("Chat starts = %+v, want the standby handed to the start", launcher.started)
	}
	// The cold path builds a new environment and bearer; adopting keeps the
	// standby's.
	if !maps.Equal(launcher.started[0].Env, launcher.prewarmed[0].Env) {
		t.Fatal("switch launched with a new environment instead of the standby's")
	}
	if !strings.HasPrefix(launcher.started[0].SystemPrompt, launcher.prewarmed[0].SystemPrompt) {
		t.Fatal("switch instructions do not extend the standby's standing instructions")
	}
}

func TestSwitchDropsStandbyForOtherSettings(t *testing.T) {
	manager, launcher, id := newStandbySwitchFixture(t)
	if err := manager.PrewarmChatSwitch(context.Background(), id,
		SwitchAgentConfig{TargetHarness: domain.HarnessCodex, Model: "picked-model"}); err != nil {
		t.Fatalf("PrewarmChatSwitch: %v", err)
	}
	if _, err := switchAgentSynchronously(context.Background(), manager, id, SwitchAgentConfig{
		TargetHarness: domain.HarnessCodex, Model: "changed-model", IdempotencyKey: "send-changed",
	}); err != nil {
		t.Fatalf("SwitchAgent: %v", err)
	}
	if len(launcher.started) != 1 || launcher.started[0].Standby != nil {
		t.Fatalf("Chat starts = %+v, want a cold start", launcher.started)
	}
	if launcher.standbys[0].discarded.Load() == 0 {
		t.Fatal("unused standby was not discarded")
	}
}

func TestPickingCurrentAgentDropsStandby(t *testing.T) {
	manager, launcher, id := newStandbySwitchFixture(t)
	if err := manager.PrewarmChatSwitch(context.Background(), id,
		SwitchAgentConfig{TargetHarness: domain.HarnessCodex}); err != nil {
		t.Fatalf("PrewarmChatSwitch: %v", err)
	}
	current := manager.store.(*switchTestStore).sessions[id].Harness
	if err := manager.PrewarmChatSwitch(context.Background(), id, SwitchAgentConfig{TargetHarness: current}); err != nil {
		t.Fatalf("PrewarmChatSwitch current: %v", err)
	}
	if len(launcher.prewarmed) != 1 || launcher.standbys[0].discarded.Load() == 0 {
		t.Fatalf("prewarmed %d, discarded %d: want the standby dropped and no new one",
			len(launcher.prewarmed), launcher.standbys[0].discarded.Load())
	}
}

func TestSwitchStopsWaitingForSlowPrewarm(t *testing.T) {
	manager, _, id := newStandbySwitchFixture(t)
	previous := chatStandbyTakeWait
	chatStandbyTakeWait = 200 * time.Millisecond
	t.Cleanup(func() { chatStandbyTakeWait = previous })
	lock := manager.chatStandbyLock(id)
	lock <- struct{}{} // a prewarm still starting its agent
	defer func() { <-lock }()
	started := time.Now()
	if got := manager.takeChatStandby(id, domain.HarnessCodex, ports.AgentConfig{}); got != nil {
		t.Fatalf("takeChatStandby = %+v, want none while the prewarm holds the session", got)
	}
	if waited := time.Since(started); waited < chatStandbyTakeWait || waited > 5*time.Second {
		t.Fatalf("waited %s, want about %s", waited, chatStandbyTakeWait)
	}
}

func TestPrewarmFinishingAfterTheSwitchGaveUpBacksOff(t *testing.T) {
	manager, launcher, id := newStandbySwitchFixture(t)
	previous := chatStandbyTakeWait
	chatStandbyTakeWait = 100 * time.Millisecond
	t.Cleanup(func() { chatStandbyTakeWait = previous })
	launcher.prewarmEntered = make(chan struct{})
	launcher.prewarmRelease = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- manager.PrewarmChatSwitch(context.Background(), id,
			SwitchAgentConfig{TargetHarness: domain.HarnessCodex})
	}()
	<-launcher.prewarmEntered
	// The switch stops waiting and starts its own agent.
	if got := manager.takeChatStandby(id, domain.HarnessCodex, ports.AgentConfig{}); got != nil {
		t.Fatalf("takeChatStandby = %+v, want none while the prewarm runs", got)
	}
	close(launcher.prewarmRelease)
	if err := <-done; err != nil {
		t.Fatalf("PrewarmChatSwitch: %v", err)
	}
	if launcher.standbys[0].discarded.Load() == 0 {
		t.Fatal("late standby was kept after the switch went ahead without it")
	}
	manager.chatStandbyMu.Lock()
	kept := manager.chatStandbys[id]
	manager.chatStandbyMu.Unlock()
	if kept != nil {
		t.Fatal("late standby was registered for the session")
	}
}

func TestSwitchWaitingForAPrewarmTakesItWhenItLands(t *testing.T) {
	manager, launcher, id := newStandbySwitchFixture(t)
	launcher.prewarmEntered = make(chan struct{})
	launcher.prewarmRelease = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- manager.PrewarmChatSwitch(context.Background(), id,
			SwitchAgentConfig{TargetHarness: domain.HarnessCodex})
	}()
	<-launcher.prewarmEntered
	rec := manager.store.(*switchTestStore).sessions[id]
	project, err := manager.loadProject(context.Background(), rec.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	agentConfig, _, err := manager.chatSwitchTargetConfig(context.Background(), rec, project,
		SwitchAgentConfig{TargetHarness: domain.HarnessCodex})
	if err != nil {
		t.Fatal(err)
	}
	// The switch is admitted (its operation registered) and waits for the prewarm.
	manager.agentOpMu.Lock()
	manager.agentOperations[id] = agentOperationSwitch
	manager.agentOpMu.Unlock()
	taken := make(chan *chatSwitchStandby, 1)
	go func() { taken <- manager.takeChatStandby(id, domain.HarnessCodex, agentConfig) }()
	time.Sleep(50 * time.Millisecond)
	close(launcher.prewarmRelease)
	if err := <-done; err != nil {
		t.Fatalf("PrewarmChatSwitch: %v", err)
	}
	if got := <-taken; got == nil || got.handle != launcher.standbys[0] {
		t.Fatalf("waiting switch took %+v, want the standby that just landed", got)
	}
	if launcher.standbys[0].discarded.Load() != 0 {
		t.Fatal("standby discarded although the switch was waiting for it")
	}
}
