package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// startRecordedTurn sends text and has the provider acknowledge it, returning
// AO's turn row for it.
func startRecordedTurn(t *testing.T, h *harness, conv *interruptRecorder, text, providerTurnID string) domain.ConversationTurn {
	t.Helper()
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: text, ClientMessageID: "client-" + text, Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("Send %q: %v", text, err)
	}
	conv.markActive(providerTurnID)
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: providerTurnID})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)[text] == domain.TurnStateRunning
	})
	return turn
}

// A Stop aimed at a turn that has since finished must not cancel the turn that
// started after it, even though that turn is now the one in flight.
func TestInterruptTurnDoesNotStopALaterTurn(t *testing.T) {
	conv := newInterruptRecorder()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	first := startRecordedTurn(t, h, conv, "first", "provider-turn-1")
	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateCompleted,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["first"] == domain.TurnStateCompleted
	})
	startRecordedTurn(t, h, conv, "second", "provider-turn-2")

	if err := h.svc.InterruptTurn(ctx, testSession, first.ID); !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("delayed Stop for a finished turn = %v, want ErrNoActiveTurn", err)
	}
	if got := conv.attemptCount(); got != 0 {
		t.Fatalf("provider interrupt attempts = %d; a delayed Stop reached the later turn", got)
	}
	snapshot := h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return true })
	if state := turnStateByText(t, snapshot)["second"]; state != domain.TurnStateRunning {
		t.Fatalf("later turn = %q, want it still running", state)
	}
}

// Stop pressed again after the first one landed answers success, not "nothing
// to stop", and does not reach the provider a second time.
func TestInterruptTurnAlreadyStoppedIsIdempotent(t *testing.T) {
	conv := newInterruptRecorder()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	turn := startRecordedTurn(t, h, conv, "long job", "provider-turn-1")
	if err := h.svc.InterruptTurn(ctx, testSession, turn.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateInterrupted,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["long job"] == domain.TurnStateInterrupted
	})

	if err := h.svc.InterruptTurn(ctx, testSession, turn.ID); err != nil {
		t.Fatalf("repeated Stop for a stopped turn = %v, want nil", err)
	}
	if got := conv.attemptCount(); got != 1 {
		t.Fatalf("provider interrupt attempts = %d, want 1", got)
	}
}

// The targeted Stop still stops the turn it names.
func TestInterruptTurnStopsTheNamedRunningTurn(t *testing.T) {
	conv := newInterruptRecorder()
	h := newHarnessWithConversation(t, conv)

	turn := startRecordedTurn(t, h, conv, "long job", "provider-turn-1")
	if err := h.svc.InterruptTurn(context.Background(), testSession, turn.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	conv.activeMu.Lock()
	attempts := append([]string(nil), conv.attempts...)
	conv.activeMu.Unlock()
	if len(attempts) != 1 || attempts[0] != "provider-turn-1" {
		t.Fatalf("provider interrupt attempts = %v, want [provider-turn-1]", attempts)
	}
}

func TestInterruptTurnRejectsAnUnknownTurn(t *testing.T) {
	conv := newInterruptRecorder()
	h := newHarnessWithConversation(t, conv)
	startRecordedTurn(t, h, conv, "long job", "provider-turn-1")

	err := h.svc.InterruptTurn(context.Background(), testSession, "not-a-turn")
	if !errors.Is(err, domain.ErrNoConversationTurn) {
		t.Fatalf("Stop for an unknown turn = %v, want ErrNoConversationTurn", err)
	}
	if got := conv.attemptCount(); got != 0 {
		t.Fatalf("provider interrupt attempts = %d, want 0", got)
	}
}

// Stop aimed at a message still waiting in the queue is not a Stop of the turn
// running ahead of it: that turn keeps running and the queue keeps its message.
func TestInterruptTurnForAQueuedMessageLeavesTheRunningTurnAlone(t *testing.T) {
	conv := newInterruptRecorder()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	startRecordedTurn(t, h, conv, "long job", "provider-turn-1")
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "next", ClientMessageID: "client-next", Origin: domain.MessageOriginHuman,
	})
	if err != nil || queued.State != domain.TurnStateQueued {
		t.Fatalf("queue behind the running turn: turn=%+v err=%v", queued, err)
	}

	if err := h.svc.InterruptTurn(ctx, testSession, queued.ID); !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("Stop for a queued message = %v, want ErrNoActiveTurn", err)
	}
	if got := conv.attemptCount(); got != 0 {
		t.Fatalf("provider interrupt attempts = %d; Stop for a queued message reached the running turn", got)
	}
	snapshot := h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return true })
	states := turnStateByText(t, snapshot)
	if states["long job"] != domain.TurnStateRunning || states["next"] != domain.TurnStateQueued {
		t.Fatalf("turn states = %v, want the running turn running and the message still queued", states)
	}
}
