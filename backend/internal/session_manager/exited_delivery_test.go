package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/wakeguard"
)

// wakingLauncher records the Chat restart the manager waits for after a send
// to an exited agent.
type wakingLauncher struct {
	*recordingLauncher
	awaitErr error
	awaits   int
}

func (l *wakingLauncher) AwaitChatWake(context.Context, domain.SessionID) error {
	l.awaits++
	return l.awaitErr
}

func exitedChatSession(t *testing.T, launcher *wakingLauncher) (*Manager, *fakeStore, domain.SessionID) {
	t.Helper()
	mgr, st, _ := newChatManager(launcher)
	rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	stored := st.sessions[rec.ID]
	stored.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: time.Now()}
	stored.ProvisionState = domain.SessionProvisionReady
	st.sessions[rec.ID] = stored
	launcher.live = false
	return mgr, st, rec.ID
}

// The reported bug: `ao send` from a worker to an orchestrator whose agent
// exited. The send relays with the wake option and waits for the restart.
func TestSendWithResultRestartsExitedChatAgent(t *testing.T) {
	launcher := &wakingLauncher{recordingLauncher: &recordingLauncher{}}
	mgr, _, id := exitedChatSession(t, launcher)

	result, err := mgr.SendWithResult(context.Background(), id, "[from worker-67] PR is up", nil,
		ports.MessageDeliveryOptions{SenderSessionID: "worker-67", WakeExitedAgent: true})
	if err != nil {
		t.Fatalf("SendWithResult = %v", err)
	}
	if result.AgentWake != AgentWakeResumed {
		t.Fatalf("AgentWake = %q, want %q", result.AgentWake, AgentWakeResumed)
	}
	last := len(launcher.relayOptions) - 1
	if last < 0 || !launcher.relayOptions[last].WakeExitedAgent {
		t.Fatalf("relay options = %+v, want the wake option passed through", launcher.relayOptions)
	}
	if launcher.awaits != 1 {
		t.Fatalf("awaits = %d, want 1", launcher.awaits)
	}
}

func TestSendWithResultReportsFailedChatRestartAsQueued(t *testing.T) {
	cause := errors.New("codex is signed out")
	launcher := &wakingLauncher{recordingLauncher: &recordingLauncher{}, awaitErr: cause}
	mgr, _, id := exitedChatSession(t, launcher)

	_, err := mgr.SendWithResult(context.Background(), id, "hello", nil, ports.MessageDeliveryOptions{WakeExitedAgent: true})
	var wakeErr *ports.AgentWakeError
	if !errors.As(err, &wakeErr) || !wakeErr.Queued || !errors.Is(wakeErr.Reason, cause) {
		t.Fatalf("SendWithResult = %v, want AgentWakeError{Queued: true} carrying the cause", err)
	}
	if errors.Is(err, ErrAgentExited) || errors.Is(err, ErrTerminated) {
		t.Fatalf("wake failure %v must not read as a plain exited/terminated refusal", err)
	}
}

// Senders that are not trusted to restart an agent (lifecycle nudges, the
// replacement notice) do not wait for or ask for a restart.
func TestSendWithoutWakeOptionDoesNotRestartExitedChatAgent(t *testing.T) {
	launcher := &wakingLauncher{recordingLauncher: &recordingLauncher{}}
	mgr, _, id := exitedChatSession(t, launcher)

	_ = mgr.Send(context.Background(), id, "CI failed", nil)
	if launcher.awaits != 0 {
		t.Fatalf("awaits = %d, want 0 for an untrusted sender", launcher.awaits)
	}
	last := len(launcher.relayOptions) - 1
	if last >= 0 && launcher.relayOptions[last].WakeExitedAgent {
		t.Fatalf("relay options = %+v, want no wake option", launcher.relayOptions[last])
	}
}

// Worker reports are a trusted sender and always restart the target agent.
func TestSendSemanticRestartsExitedChatOrchestrator(t *testing.T) {
	launcher := &wakingLauncher{recordingLauncher: &recordingLauncher{}}
	mgr, _, id := exitedChatSession(t, launcher)

	if err := mgr.SendSemantic(context.Background(), id, "Reports since your previous turn: ...", "report-batch-1"); err != nil {
		t.Fatalf("SendSemantic = %v", err)
	}
	last := len(launcher.relayOptions) - 1
	if last < 0 || !launcher.relayOptions[last].WakeExitedAgent || launcher.relayIDs[last] != "report-batch-1" {
		t.Fatalf("relay = %+v / %v, want the batch relayed with the wake option", launcher.relayOptions, launcher.relayIDs)
	}
	if launcher.awaits != 1 {
		t.Fatalf("awaits = %d, want 1", launcher.awaits)
	}
}

// A terminated session is never restarted by a message.
func TestSendWithWakeOptionStillRefusesTerminatedChatSession(t *testing.T) {
	launcher := &wakingLauncher{recordingLauncher: &recordingLauncher{}}
	mgr, st, id := exitedChatSession(t, launcher)
	stored := st.sessions[id]
	stored.IsTerminated = true
	st.sessions[id] = stored

	_, err := mgr.SendWithResult(context.Background(), id, "hello", nil, ports.MessageDeliveryOptions{WakeExitedAgent: true})
	if !errors.Is(err, ErrTerminated) {
		t.Fatalf("SendWithResult = %v, want ErrTerminated", err)
	}
	if launcher.awaits != 0 {
		t.Fatalf("awaits = %d, want 0", launcher.awaits)
	}
}

// A terminal session has no durable queue: a failed restart returns a typed
// error saying the message was not delivered, never writes to the pane, and
// the next sender inside the cooldown gets the same answer without another
// restart attempt.
func TestTerminalWakeFailureIsTypedAndCoolsDown(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = domain.SessionRecord{
		ID: "s1", ProjectID: "missing-project", Harness: "claude-code", Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	msg := &fakeMessenger{}
	m := newSendTestManager(t, signalingAgent{}, msg, st)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := m.SendWithResult(ctx, "s1", "status?", nil, ports.MessageDeliveryOptions{WakeExitedAgent: true})
	var wakeErr *ports.AgentWakeError
	if !errors.As(err, &wakeErr) || wakeErr.Queued {
		t.Fatalf("first send = %v, want AgentWakeError{Queued: false}", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("pane writes = %d, want none after a failed restart", len(msg.msgs))
	}

	_, err = m.SendWithResult(ctx, "s1", "status?", nil, ports.MessageDeliveryOptions{WakeExitedAgent: true})
	if !errors.As(err, &wakeErr) || !errors.Is(wakeErr.Reason, wakeguard.ErrRecentFailure) {
		t.Fatalf("second send = %v, want the cooldown refusal", err)
	}
}

// The agent restarted but the queue hand-off is still outstanding: the message
// is queued, not delivered, so the sender hears "resuming", not "resumed".
func TestSendWithResultReportsPendingHandOffAsResuming(t *testing.T) {
	launcher := &wakingLauncher{recordingLauncher: &recordingLauncher{},
		awaitErr: fmt.Errorf("%w: drain failed", ports.ErrAgentWakeDeliveryPending)}
	mgr, _, id := exitedChatSession(t, launcher)

	result, err := mgr.SendWithResult(context.Background(), id, "hello", nil, ports.MessageDeliveryOptions{WakeExitedAgent: true})
	if err != nil {
		t.Fatalf("SendWithResult = %v, want the queued message accepted", err)
	}
	if result.AgentWake != AgentWakeResuming {
		t.Fatalf("AgentWake = %q, want %q", result.AgentWake, AgentWakeResuming)
	}
}

// A terminal agent accepted a report and exited before the acceptance was
// read. The coordinator's retry must find the receipt for that launch, not
// restart the agent into a new launch and send the report again.
func TestSendSemanticDoesNotRestartForReportAcceptedBeforeExit(t *testing.T) {
	const deliveryID = "report-batch:accepted-before-exit"
	st := newFakeStore()
	rec := pastStartupGate(domain.SessionRecord{ID: "s1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI})
	rec.Activity = domain.Activity{State: domain.ActivityExited}
	rec.ProvisionState = domain.SessionProvisionReady
	rec.Metadata.RuntimeLaunchID = "launch-1"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointCoordination
	rec.Metadata.ConversationCheckpointGeneration = "launch-1"
	rec.Metadata.ConversationCheckpointTurnID = deliveryID
	st.sessions["s1"] = rec
	msg := &fakeMessenger{}
	m := newSendTestManager(t, semanticSignalingAgent{}, msg, st)

	if err := m.SendSemantic(context.Background(), "s1", "report", deliveryID); err != nil {
		t.Fatalf("SendSemantic = %v, want the earlier acceptance honored", err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("pane writes = %d, want none for an accepted report", len(msg.msgs))
	}
	if got := st.sessions["s1"].Metadata.RuntimeLaunchID; got != "launch-1" {
		t.Fatalf("runtime launch = %q, want the agent left exited on launch-1", got)
	}
}
