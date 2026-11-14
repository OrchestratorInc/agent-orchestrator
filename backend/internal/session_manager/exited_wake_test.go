package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The reported bug: `ao send` from a worker to an orchestrator whose agent
// exited failed. A send from the user or another AO session resumes it first.
func TestSendRestartsExitedAgentForTrustedSender(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, store, _ := newChatManager(launcher)
	seedChatResumeSession(store, domain.ActivityExited)

	err := mgr.SendWithOptions(context.Background(), "mer-1", "PR is up", nil,
		ports.MessageDeliveryOptions{SenderSessionID: "worker-67", WakeExitedAgent: true})
	if err != nil {
		t.Fatalf("SendWithOptions = %v", err)
	}
	if len(launcher.started) != 1 || len(launcher.relayed) != 1 || launcher.relayed[0] != "PR is up" {
		t.Fatalf("started=%d relayed=%v, want one restart and the message delivered", len(launcher.started), launcher.relayed)
	}
}

// Automated nudges (CI, review comments) never restart an exited agent.
func TestSendWithoutWakeOptionLeavesExitedAgentStopped(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, store, _ := newChatManager(launcher)
	seedChatResumeSession(store, domain.ActivityExited)

	_ = mgr.Send(context.Background(), "mer-1", "CI failed", nil)
	if len(launcher.started) != 0 {
		t.Fatalf("started = %d, want no restart for an automated nudge", len(launcher.started))
	}
}

// A failed restart is reported, and the next message within a minute does
// not try again, so retried worker reports cannot spin restarts.
func TestFailedRestartIsNotRetriedImmediately(t *testing.T) {
	launcher := &recordingLauncher{startErr: errors.New("codex is signed out")}
	mgr, store, _ := newChatManager(launcher)
	seedChatResumeSession(store, domain.ActivityExited)
	opts := ports.MessageDeliveryOptions{SenderSessionID: "worker-67", WakeExitedAgent: true}

	if err := mgr.SendWithOptions(context.Background(), "mer-1", "one", nil, opts); err == nil {
		t.Fatal("first send succeeded, want the restart failure")
	}
	attempts := len(launcher.started)
	if err := mgr.SendWithOptions(context.Background(), "mer-1", "two", nil, opts); !errors.Is(err, ErrAgentExited) {
		t.Fatalf("second send = %v, want ErrAgentExited during the cooldown", err)
	}
	if len(launcher.started) != attempts || len(launcher.relayed) != 0 {
		t.Fatalf("started=%d (was %d) relayed=%v, want no second restart and nothing sent", len(launcher.started), attempts, launcher.relayed)
	}
}

// A terminated session still refuses.
func TestSendDoesNotRestartTerminatedSession(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, store, _ := newChatManager(launcher)
	seedChatResumeSession(store, domain.ActivityExited)
	rec := store.sessions["mer-1"]
	rec.IsTerminated = true
	store.sessions["mer-1"] = rec

	_ = mgr.SendWithOptions(context.Background(), "mer-1", "hi", nil, ports.MessageDeliveryOptions{WakeExitedAgent: true})
	if len(launcher.started) != 0 {
		t.Fatalf("started = %d, want no restart for a terminated session", len(launcher.started))
	}
}
