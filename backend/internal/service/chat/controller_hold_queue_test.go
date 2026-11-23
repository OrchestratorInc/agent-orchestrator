package chat_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// assertTurnStaysQueued fails if the named turn leaves queued within a short settle
// window. A drain that should not have run would move it to running almost at once.
func assertTurnStaysQueued(t *testing.T, h *harness, text string) {
	t.Helper()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
		if err != nil {
			t.Fatalf("load snapshot: %v", err)
		}
		if got := turnStateByText(t, snapshot)[text]; got != domain.TurnStateQueued {
			t.Fatalf("queued turn %q became %q; a non-success terminal turn must hold the queue", text, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A failed primary turn must not release queued work into the same outage; only a
// completed turn may. Regression for issue #4861.
func TestFailedPrimaryTurnHoldsQueuedWork(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "root", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send root: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateRunning
	})

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "automation", ClientMessageID: "c2", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatalf("queue automation: %v", err)
	}

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateFailed,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateFailed
	})

	assertTurnStaysQueued(t, h, "automation")
	if got := h.conv.sentTexts(); len(got) != 1 || got[0] != "root" {
		t.Fatalf("provider received %v, want only the root prompt", got)
	}
}

// A recovered turn is terminal but carries no portable outcome, so it is no proof
// the next turn will succeed. It holds the queue like a failed turn.
func TestRecoveredPrimaryTurnHoldsQueuedWork(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "root", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send root: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateRunning
	})

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "automation", ClientMessageID: "c2", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatalf("queue automation: %v", err)
	}

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateRecovered,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateRecovered
	})

	assertTurnStaysQueued(t, h, "automation")
	if got := h.conv.sentTexts(); len(got) != 1 || got[0] != "root" {
		t.Fatalf("provider received %v, want only the root prompt", got)
	}
}

// A prompt the host's watchdog failed for silence never reached an answer, so it
// is no sign the provider is down. Queued work gets its own turn instead of
// waiting for the user to type again. A provider error still holds the queue.
func TestStalledPrimaryTurnReleasesQueuedWork(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		released bool
	}{
		{"watchdog stall releases the queue", fmt.Errorf("ACP session/prompt: %w", ports.ErrChatPromptStalled), true},
		{"provider error holds the queue", errors.New("ACP session/prompt: provider unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "root", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
			}); err != nil {
				t.Fatalf("send root: %v", err)
			}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["root"] == domain.TurnStateRunning
			})
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "automation", ClientMessageID: "c2", Origin: domain.MessageOriginAutomation,
			}); err != nil {
				t.Fatalf("queue automation: %v", err)
			}

			h.conv.emit(ports.ChatEvent{
				Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
				TurnState: domain.TurnStateFailed, Err: tc.err,
			})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["root"] == domain.TurnStateFailed
			})

			if !tc.released {
				assertTurnStaysQueued(t, h, "automation")
				return
			}
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["automation"] != domain.TurnStateQueued
			})
			if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "automation" {
				t.Fatalf("provider received %v, want the root prompt then the queued one", got)
			}
		})
	}
}

func TestUsageLimitHoldWaitsForTheUsersSend(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	failUsageLimitedTurnBehindQueue(t, h, "queued note")

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "another note", ClientMessageID: "c-auto", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatal(err)
	}
	assertTurnStaysQueued(t, h, "queued note")

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "carry on", ClientMessageID: "c-user", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["queued note"] == domain.TurnStateRunning
	})
	if got := turnStateByText(t, snapshot)["carry on"]; got != domain.TurnStateQueued {
		t.Fatalf("user message is %q, want queued behind the earlier notes", got)
	}
}

func TestUsageLimitOnAutomationPromptLeavesTheQueueUnheld(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "automation task", ClientMessageID: "c1", Origin: domain.MessageOriginAutomation,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "queued note", ClientMessageID: "c2", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, turn.ID)

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "another note", ClientMessageID: "c3", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatal(err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["queued note"] == domain.TurnStateRunning
	})
}

func TestInterfaceHandoffDrainsTheHeldQueue(t *testing.T) {
	h := newHarness(t)
	failUsageLimitedTurnBehindQueue(t, h, "queued note")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prepared := make(chan error, 1)
	go func() { prepared <- h.svc.PrepareChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionDrain) }()
	var running string
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		running = ""
		for _, turn := range s.Turns {
			if turn.State == domain.TurnStateRunning {
				running = turn.ProviderTurnID
			}
		}
		return running != "" && turnStateByText(t, s)["queued note"] == domain.TurnStateRunning
	})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: running, TurnState: domain.TurnStateCompleted})
	if err := <-prepared; err != nil {
		t.Fatalf("prepare handoff: %v", err)
	}
}

func TestCompletedManualAccountMoveReleasesTheHeldQueue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	failUsageLimitedTurnBehindQueue(t, h, "queued note")
	if err := h.svc.DrainQueued(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	assertTurnStaysQueued(t, h, "queued note")
	if err := h.svc.ReleaseQueueAfterAccountMove(ctx, testSession, "manual-move"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DrainQueued(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["queued note"] == domain.TurnStateRunning
	})
}
