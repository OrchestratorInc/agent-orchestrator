package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// Generation-pinned send and stop.
//
// The gateway observes a controller generation and then asks the chat service to act on
// it. Between those two moments the controller can be replaced. These tests pin the
// refusal that makes the observation meaningful, and the positive control that proves the
// fence is a fence rather than a wall.

// pinnedSendMessage is a message a workflow would hand to a worker.
func pinnedSendMessage(clientMessageID string) ports.ChatUserMessage {
	return ports.ChatUserMessage{
		Text:            "keep going, but skip the migration",
		ClientMessageID: clientMessageID,
		Origin:          domain.MessageOriginAutomation,
	}
}

// TestSendPinnedDispatchesAtTheCurrentGeneration is the positive control: with the live
// generation the message reaches the provider exactly once.
func TestSendPinnedDispatchesAtTheCurrentGeneration(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	turn, err := h.svc.SendPinned(ctx, testSession, h.ctrl.Generation(), pinnedSendMessage("pinned-send-1"))
	if err != nil {
		t.Fatalf("SendPinned: %v", err)
	}
	if turn.ID == "" {
		t.Fatal("SendPinned recorded no turn")
	}
	if turn.State != domain.TurnStateRunning {
		t.Fatalf("turn state = %q, want running", turn.State)
	}
	if calls := h.conv.sendCallCount(); calls != 1 {
		t.Fatalf("provider sends = %d, want 1", calls)
	}
}

// TestSendPinnedRefusesAStaleGeneration is the refusal that gives the pin its meaning: a
// controller the caller no longer owns must not send.
func TestSendPinnedRefusesAStaleGeneration(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	_, err := h.svc.SendPinned(ctx, testSession, "generation-from-a-dead-controller",
		pinnedSendMessage("pinned-send-stale"))
	if !errors.Is(err, chatsvc.ErrStaleControllerGeneration) {
		t.Fatalf("error = %v, want ErrStaleControllerGeneration", err)
	}
	if calls := h.conv.sendCallCount(); calls != 0 {
		t.Fatalf("provider sends = %d, want 0: a stale generation reached the provider", calls)
	}

	// Nothing durable was recorded either: the refusal is about the caller's evidence, so
	// the same client message id must still be deliverable once the evidence is right.
	if _, err := h.svc.SendPinned(ctx, testSession, h.ctrl.Generation(),
		pinnedSendMessage("pinned-send-stale")); err != nil {
		t.Fatalf("SendPinned after correcting the generation: %v", err)
	}
	if calls := h.conv.sendCallCount(); calls != 1 {
		t.Fatalf("provider sends = %d, want 1 after the fence was satisfied", calls)
	}
}

// TestSendPinnedRefusesAnEmptyGeneration proves "no expectation" is not accepted on the
// pinned path: a caller that cannot name its controller cannot be fenced.
func TestSendPinnedRefusesAnEmptyGeneration(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	_, err := h.svc.SendPinned(ctx, testSession, "", pinnedSendMessage("pinned-send-empty"))
	if !errors.Is(err, chatsvc.ErrStaleControllerGeneration) {
		t.Fatalf("error = %v, want ErrStaleControllerGeneration", err)
	}
	if calls := h.conv.sendCallCount(); calls != 0 {
		t.Fatalf("provider sends = %d, want 0", calls)
	}
}

// TestSendPinnedIsIdempotentPerClientMessageID proves a retried client message id is
// answered as a duplicate and never dispatched a second time.
func TestSendPinnedIsIdempotentPerClientMessageID(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	generation := h.ctrl.Generation()

	first, err := h.svc.SendPinned(ctx, testSession, generation, pinnedSendMessage("pinned-send-dup"))
	if err != nil {
		t.Fatalf("first SendPinned: %v", err)
	}
	if first.ID == "" {
		t.Fatal("first SendPinned recorded no turn")
	}
	retry, err := h.svc.SendPinned(ctx, testSession, generation, pinnedSendMessage("pinned-send-dup"))
	if err != nil {
		t.Fatalf("retried SendPinned: %v", err)
	}
	if retry.ID != "" {
		t.Fatalf("retry produced turn %q, want the empty duplicate answer", retry.ID)
	}
	if calls := h.conv.sendCallCount(); calls != 1 {
		t.Fatalf("provider sends = %d, want exactly 1 for a duplicated client message id", calls)
	}
}

// TestInterruptPinnedConfirmsAtTheCurrentGeneration is the stop positive control.
func TestInterruptPinnedConfirmsAtTheCurrentGeneration(t *testing.T) {
	h, conv := interruptHarness(t)
	ctx := context.Background()

	if err := h.svc.InterruptPinned(ctx, testSession, h.ctrl.Generation()); err != nil {
		t.Fatalf("InterruptPinned: %v", err)
	}
	if conv.attemptCount() != 1 {
		t.Fatalf("provider stop attempts = %d, want 1", conv.attemptCount())
	}
}

// TestInterruptPinnedRefusesAStaleGeneration proves a stop aimed at a replaced controller
// never reaches the provider.
func TestInterruptPinnedRefusesAStaleGeneration(t *testing.T) {
	h, conv := interruptHarness(t)
	ctx := context.Background()

	err := h.svc.InterruptPinned(ctx, testSession, "generation-from-a-dead-controller")
	if !errors.Is(err, chatsvc.ErrStaleControllerGeneration) {
		t.Fatalf("error = %v, want ErrStaleControllerGeneration", err)
	}
	if conv.attemptCount() != 0 {
		t.Fatalf("provider stop attempts = %d, want 0", conv.attemptCount())
	}
}

// TestInterruptPinnedRefusesAnEmptyGeneration proves an unfenced stop is refused.
func TestInterruptPinnedRefusesAnEmptyGeneration(t *testing.T) {
	h, conv := interruptHarness(t)

	if err := h.svc.InterruptPinned(context.Background(), testSession, ""); !errors.Is(
		err, chatsvc.ErrStaleControllerGeneration) {
		t.Fatalf("error = %v, want ErrStaleControllerGeneration", err)
	}
	if conv.attemptCount() != 0 {
		t.Fatalf("provider stop attempts = %d, want 0", conv.attemptCount())
	}
}

// TestUnpinnedSendAndInterruptStillWork proves the refactor did not change the existing
// entry points: they pass no fence and behave as before.
func TestUnpinnedSendAndInterruptStillWork(t *testing.T) {
	h, conv := interruptHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, pinnedSendMessage("unpinned-send-1")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := h.svc.Interrupt(ctx, testSession); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if conv.attemptCount() != 1 {
		t.Fatalf("provider stop attempts = %d, want 1", conv.attemptCount())
	}
}

// interruptHarness builds a harness whose provider records interrupt attempts and marks
// the running turn active, so a stop can succeed rather than answering "no active turn".
func interruptHarness(t *testing.T) (*harness, *interruptRecorder) {
	t.Helper()
	conv := newInterruptRecorder()
	h := newHarnessWithConversation(t, conv)
	// The provider announces the turn, which is what makes it interruptible: a turn the
	// provider has not acknowledged cannot be stopped.
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1",
	})
	conv.markActive("provider-turn-1")
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "start something long", ClientMessageID: "long-1",
		Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("seed Send: %v", err)
	}
	conv.markActive("provider-turn-1")
	return h, conv
}
