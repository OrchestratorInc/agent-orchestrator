package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// singleHostChatLauncher models the session's one persistent provider host: a
// start that failed after launching still owns the host, and the next start of
// another harness is refused until StopChat shuts that host down.
type singleHostChatLauncher struct {
	*switchAgentChatLauncher
	failHarness domain.AgentHarness
	hostOwner   domain.AgentHarness
}

func (l *singleHostChatLauncher) StartChat(ctx context.Context, cfg ChatStart) (ChatStarted, error) {
	if l.hostOwner != "" && l.hostOwner != cfg.Harness {
		l.started = append(l.started, cfg)
		return ChatStarted{}, fmt.Errorf("chat host incompatible: host=%s requested=%s", l.hostOwner, cfg.Harness)
	}
	if cfg.Harness == l.failHarness {
		l.started = append(l.started, cfg)
		l.hostOwner = cfg.Harness
		return ChatStarted{}, errors.New("target provider exited during initialize")
	}
	return l.switchAgentChatLauncher.StartChat(ctx, cfg)
}

func (l *singleHostChatLauncher) StopChat(ctx context.Context, id domain.SessionID) error {
	l.hostOwner = ""
	return l.switchAgentChatLauncher.StopChat(ctx, id)
}

func chatSwitchSource(t *testing.T) (*Manager, *switchTestStore, domain.SessionRecord) {
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
	return manager, store, rec
}

// A Chat target that fails to start leaves its host behind. Rollback stops it
// before reopening the source, so the source is restored and the switch ends
// failed instead of parking the session on an unconfirmed source restore.
func TestSwitchAgentChatTargetFailureStopsTargetHostBeforeRestoringSource(t *testing.T) {
	manager, store, rec := chatSwitchSource(t)
	launcher := &singleHostChatLauncher{
		switchAgentChatLauncher: &switchAgentChatLauncher{recordingLauncher: &recordingLauncher{}, store: store, live: true},
		failHarness:             domain.HarnessCodex,
	}
	manager.chat = launcher

	sw, err := switchAgentSynchronously(context.Background(), manager, rec.ID, SwitchAgentConfig{
		TargetHarness:  domain.HarnessCodex,
		IdempotencyKey: "chat-target-host-left-behind",
	})
	if err == nil {
		t.Fatal("SwitchAgent succeeded with a failing target")
	}
	if !sw.State.Terminal() || sw.RequiresSourceRestore() {
		t.Fatalf("switch = state %q code %q, want terminal failure with the source restored", sw.State, sw.ErrorCode)
	}
	got := store.sessions[rec.ID]
	if got.Harness != domain.HarnessClaudeCode || got.Metadata.ProviderConversationID != "source-chat-native" {
		t.Fatalf("source not restored: %+v", got)
	}
	if manager.SessionMutationInProgress(rec.ID) {
		t.Fatal("restored source left the switch gate closed")
	}
}

// A switch parked on an unconfirmed source restore holds no running work, so
// Kill takes over its gate, terminates the session, and closes the switch.
// Before, the retained gate refused Kill with "agent switch in progress"
// forever, including after restarts, so the session could not be archived and
// its project could not be removed.
func TestKillTerminatesSessionQuarantinedOnSourceRestore(t *testing.T) {
	runtime := &switchRollbackCancellationRuntime{
		fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{
			createIDs: []string{"target-handle"},
		}},
		rollbackErr: errors.New("source relaunch unavailable"),
	}
	manager, store, _ := newSwitchTestManager(t, runtime)
	sw, err := switchAgentSynchronously(context.Background(), manager, "proj-1", SwitchAgentConfig{
		TargetHarness:  domain.HarnessCodex,
		IdempotencyKey: "kill-quarantined-source-restore",
	})
	if err == nil || !sw.RequiresSourceRestore() || !manager.SessionMutationInProgress("proj-1") {
		t.Fatalf("fixture = err %v state %q code %q gated %v, want retained source restore",
			err, sw.State, sw.ErrorCode, manager.SessionMutationInProgress("proj-1"))
	}

	if _, err := manager.Kill(context.Background(), "proj-1"); err != nil {
		t.Fatalf("Kill of a quarantined session: %v", err)
	}
	if !store.sessions["proj-1"].IsTerminated {
		t.Fatal("Kill did not terminate the quarantined session")
	}
	settled := store.switches[sw.ID]
	if settled.State != domain.AgentSwitchFailed || settled.ErrorCode != domain.AgentSwitchErrorSourceSessionTerminated {
		t.Fatalf("switch after Kill = state %q code %q, want failed/source_session_terminated", settled.State, settled.ErrorCode)
	}
	if manager.SessionMutationInProgress("proj-1") {
		t.Fatal("Kill left the session gate closed")
	}
}

// A switch retained for any other reason may still have a live process whose
// ownership is unknown, so Kill keeps refusing it.
func TestKillStillRefusesSwitchWithUnconfirmedSourceStop(t *testing.T) {
	runtime := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{destroyErr: errors.New("teardown unavailable")}}
	manager, store, _ := newSwitchTestManager(t, runtime)
	sw, err := switchAgentSynchronously(context.Background(), manager, "proj-1", SwitchAgentConfig{
		TargetHarness: domain.HarnessCodex, IdempotencyKey: "kill-unconfirmed-source-stop",
	})
	if !errors.Is(err, ErrSwitchSourceStopUnconfirmed) || !sw.RequiresSourceStopRecovery() {
		t.Fatalf("fixture = err %v state %q code %q, want retained source-stop recovery", err, sw.State, sw.ErrorCode)
	}

	if _, err := manager.Kill(context.Background(), "proj-1"); !errors.Is(err, ErrSwitchInProgress) {
		t.Fatalf("Kill = %v, want ErrSwitchInProgress", err)
	}
	if store.sessions["proj-1"].IsTerminated || store.switches[sw.ID].State.Terminal() {
		t.Fatal("refused Kill changed the session or switch")
	}
	if !manager.SessionMutationInProgress("proj-1") {
		t.Fatal("refused Kill released the switch gate")
	}
}
