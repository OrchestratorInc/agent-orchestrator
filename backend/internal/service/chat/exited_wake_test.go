package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/wakeguard"
)

// exitedChatHarness leaves testSession open with its agent exited: the
// controller stopped and the durable activity says so, as after a desktop
// quit, a provider crash, or `ao session exit-agent`.
func exitedChatHarness(t *testing.T) *harness {
	t.Helper()
	h, _ := settledHibernationHarness(t, domain.TurnStateCompleted)
	ctx := context.Background()
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatalf("stop controller: %v", err)
	}
	rec, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("get session = %v, %v", found, err)
	}
	rec.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: h.now()}
	if err := h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	return h
}

// exitedWakeService is a fresh Chat service over the harness store, as the
// daemon would hold after the agent exited. wake runs in place of Session
// Manager's resume; start starts the controller the resume would.
func exitedWakeService(t *testing.T, h *harness, resumed *fakeConversation, wake func(ctx context.Context, start func() error) error) *chatsvc.Service {
	t.Helper()
	var nextID atomic.Int32
	svc := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: resumed}},
		Activity: h.activity, Log: slog.New(slog.DiscardHandler), Now: h.now,
		NewID: func() string { return fmt.Sprintf("exited-wake-%d", nextID.Add(1)) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	svc.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		return wake(ctx, func() error {
			rec, _, err := h.st.GetSession(ctx, id)
			if err != nil {
				return err
			}
			_, err = svc.Start(ctx, chatsvc.StartConfig{
				SessionID: id, ProjectID: testProject, Harness: domain.HarnessCodex,
				WorkspacePath: t.TempDir(), ProviderConversationID: rec.Metadata.ProviderConversationID,
			})
			return err
		})
	})
	return svc
}

func queuedTurnCount(t *testing.T, h *harness) int {
	t.Helper()
	ctx := context.Background()
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	queued := 0
	for _, turn := range snapshot.Turns {
		if turn.State == domain.TurnStateQueued {
			queued++
		}
	}
	return queued
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The reported bug: a worker's `ao send` to an orchestrator whose agent exited
// failed instead of waking it. Many senders at once must cause one restart.
func TestRelayToExitedAgentQueuesAndRestartsOnceForConcurrentSenders(t *testing.T) {
	h := exitedChatHarness(t)
	ctx := context.Background()
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	release := make(chan struct{})
	var wakes atomic.Int32
	svc := exitedWakeService(t, h, resumed, func(ctx context.Context, start func() error) error {
		wakes.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return start()
	})

	const senders = 5
	var wg sync.WaitGroup
	errs := make(chan error, senders)
	for i := range senders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.RelaySessionChatTurn(ctx, testSession, fmt.Sprintf("report %d", i), "",
				ports.MessageDeliveryOptions{SenderSessionID: "worker-67", WakeExitedAgent: true})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("relay to exited agent = %v, want queued", err)
		}
	}
	if got := queuedTurnCount(t, h); got != senders {
		t.Fatalf("queued turns = %d, want %d durable before the agent is up", got, senders)
	}
	if got := resumed.sentTexts(); len(got) != 0 {
		t.Fatalf("provider received %v before the restart finished", got)
	}

	close(release)
	if err := svc.AwaitChatWake(ctx, testSession); err != nil {
		t.Fatalf("AwaitChatWake = %v", err)
	}
	waitFor(t, "the first queued message to reach the restarted agent", func() bool { return len(resumed.sentTexts()) > 0 })
	if got := wakes.Load(); got != 1 {
		t.Fatalf("restarts = %d, want exactly one for %d concurrent senders", got, senders)
	}
}

// A sender that is not trusted to restart the agent (a lifecycle nudge) keeps
// today's refusal and queues nothing.
func TestRelayWithoutWakeOptionStillRefusesExitedAgent(t *testing.T) {
	h := exitedChatHarness(t)
	var wakes atomic.Int32
	svc := exitedWakeService(t, h, newFakeConversation(), func(context.Context, func() error) error {
		wakes.Add(1)
		return nil
	})
	_, err := svc.RelaySessionChatTurn(context.Background(), testSession, "CI failed", "", ports.MessageDeliveryOptions{})
	if !errors.Is(err, chatsvc.ErrNoController) {
		t.Fatalf("relay without wake option = %v, want ErrNoController", err)
	}
	if wakes.Load() != 0 || queuedTurnCount(t, h) != 0 {
		t.Fatalf("restarts = %d, queued = %d; want neither", wakes.Load(), queuedTurnCount(t, h))
	}
}

// A failed restart (harness missing, auth, workspace gone) keeps the message
// queued, reports why, and is not retried by the next sender inside the
// cooldown, so a project full of reporting workers cannot spin restarts.
func TestFailedRestartKeepsMessageQueuedAndCoolsDown(t *testing.T) {
	h := exitedChatHarness(t)
	ctx := context.Background()
	var wakes atomic.Int32
	cause := errors.New("codex is not installed")
	svc := exitedWakeService(t, h, newFakeConversation(), func(context.Context, func() error) error {
		wakes.Add(1)
		return cause
	})

	if _, err := svc.RelaySessionChatTurn(ctx, testSession, "report", "report-batch-1",
		ports.MessageDeliveryOptions{WakeExitedAgent: true}); err != nil {
		t.Fatalf("relay = %v, want queued", err)
	}
	if err := svc.AwaitChatWake(ctx, testSession); !errors.Is(err, cause) {
		t.Fatalf("AwaitChatWake = %v, want the restart failure", err)
	}
	if got := queuedTurnCount(t, h); got != 1 {
		t.Fatalf("queued turns after a failed restart = %d, want the message kept", got)
	}

	// The same idempotency key (a report retry) neither duplicates the turn nor
	// restarts again inside the cooldown.
	if _, err := svc.RelaySessionChatTurn(ctx, testSession, "report", "report-batch-1",
		ports.MessageDeliveryOptions{WakeExitedAgent: true}); err != nil {
		t.Fatalf("retried relay = %v", err)
	}
	if err := svc.AwaitChatWake(ctx, testSession); !errors.Is(err, wakeguard.ErrRecentFailure) || !errors.Is(err, cause) {
		t.Fatalf("AwaitChatWake inside cooldown = %v, want ErrRecentFailure wrapping the cause", err)
	}
	if got := wakes.Load(); got != 1 {
		t.Fatalf("restarts = %d, want 1 inside the cooldown", got)
	}
	if got := queuedTurnCount(t, h); got != 1 {
		t.Fatalf("queued turns = %d, want the retry deduplicated", got)
	}
}

// `ao send --steer` to an exited agent has no turn to steer: it queues,
// restarts the agent, and says so instead of failing with "no controller".
func TestSteerOrSendToExitedAgentRestartsIt(t *testing.T) {
	h := exitedChatHarness(t)
	ctx := context.Background()
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	var wakes atomic.Int32
	svc := exitedWakeService(t, h, resumed, func(_ context.Context, start func() error) error {
		wakes.Add(1)
		return start()
	})

	result, err := svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{
		Text: "status?", ClientMessageID: "steer-1", SenderSessionID: "worker-67",
	}, false)
	if err != nil {
		t.Fatalf("SteerOrSend = %v", err)
	}
	if !result.Woke || result.Steered || result.Turn.ID == "" {
		t.Fatalf("result = %+v, want a queued turn on a restarted agent", result)
	}
	if wakes.Load() != 1 {
		t.Fatalf("restarts = %d, want 1", wakes.Load())
	}
	waitFor(t, "the steered message to reach the restarted agent", func() bool { return len(resumed.sentTexts()) > 0 })
}

// A failed restart through steer-or-send reports a typed error that says the
// message is still queued.
func TestSteerOrSendToExitedAgentReportsTypedFailure(t *testing.T) {
	h := exitedChatHarness(t)
	svc := exitedWakeService(t, h, newFakeConversation(), func(context.Context, func() error) error {
		return errors.New("workspace is gone")
	})
	_, err := svc.SteerOrSend(context.Background(), testSession, ports.ChatUserMessage{
		Text: "status?", ClientMessageID: "steer-2",
	}, false)
	var wakeErr *ports.AgentWakeError
	if !errors.As(err, &wakeErr) || !wakeErr.Queued || !errors.Is(err, ports.ErrAgentWakeFailed) {
		t.Fatalf("SteerOrSend = %v, want AgentWakeError{Queued: true}", err)
	}
	if got := queuedTurnCount(t, h); got != 1 {
		t.Fatalf("queued turns = %d, want the message kept", got)
	}
}

// A terminated session is never restarted by a message.
func TestRelayToTerminatedSessionIsRefused(t *testing.T) {
	h := exitedChatHarness(t)
	ctx := context.Background()
	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	var wakes atomic.Int32
	svc := exitedWakeService(t, h, newFakeConversation(), func(context.Context, func() error) error {
		wakes.Add(1)
		return nil
	})
	if _, err := svc.RelaySessionChatTurn(ctx, testSession, "hi", "", ports.MessageDeliveryOptions{WakeExitedAgent: true}); err == nil {
		t.Fatal("relay to a terminated session succeeded")
	}
	if wakes.Load() != 0 {
		t.Fatalf("restarts = %d, want none for a terminated session", wakes.Load())
	}
}

// A restart that times out on its own is a failure even though the sender is
// still waiting; only the sender's own request ending reads as "pending".
func TestSteerOrSendTellsRestartTimeoutFromCallerLeaving(t *testing.T) {
	t.Run("restart timed out", func(t *testing.T) {
		h := exitedChatHarness(t)
		svc := exitedWakeService(t, h, newFakeConversation(), func(context.Context, func() error) error {
			return context.DeadlineExceeded
		})
		result, err := svc.SteerOrSend(context.Background(), testSession, ports.ChatUserMessage{
			Text: "status?", ClientMessageID: "steer-timeout",
		}, false)
		var wakeErr *ports.AgentWakeError
		if !errors.As(err, &wakeErr) || !wakeErr.Queued || result.Woke || result.WakePending {
			t.Fatalf("SteerOrSend = %+v, %v; want AgentWakeError{Queued: true}", result, err)
		}
	})
	t.Run("caller stopped waiting", func(t *testing.T) {
		h := exitedChatHarness(t)
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		// The restart outlives this test, so it ends in a failure rather than
		// starting a controller after the test's temp dirs are gone.
		svc := exitedWakeService(t, h, newFakeConversation(), func(context.Context, func() error) error {
			<-release
			return errors.New("test finished")
		})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		result, err := svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{
			Text: "status?", ClientMessageID: "steer-left",
		}, false)
		if err != nil {
			t.Fatalf("SteerOrSend = %v, want the queued message accepted", err)
		}
		if result.Woke || !result.WakePending || result.Turn.ID == "" {
			t.Fatalf("result = %+v, want a pending restart, not a completed one", result)
		}
	})
}
