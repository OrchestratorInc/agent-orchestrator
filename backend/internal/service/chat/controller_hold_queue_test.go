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
