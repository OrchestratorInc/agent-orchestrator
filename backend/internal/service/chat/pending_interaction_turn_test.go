package chat_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// A pending interaction stored without a turn is a row the chat UI never
// renders — the timeline hides pending questions and the composer dock filters
// on the turn — so the provider blocks on an answer nobody can send (#7658).
// A replay that reaches the controller before its turn correlation is restored
// must anchor to the turn in flight instead of landing unlinked.
func TestPendingInteractionAnchorsToTheRunningTurnWhenProviderOmitsIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "work that stops to ask a question", ClientMessageID: "question-turn-1",
		Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	h.conv.emit(
		ports.ChatEvent{
			Kind: ports.ChatEventInputRequested, ProviderTurnID: "",
			RequestID: "replayed-elicitation", Summary: "Questions",
			Input: &ports.ChatInputRequest{
				Mode: ports.ChatInputModeForm, Message: "Questions",
				Schema: map[string]any{"type": "object"},
			},
		},
		ports.ChatEvent{
			Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "",
			ProviderItemID: "replayed-approval", RequestID: "replayed-approval-request",
			Summary: "Approve the rerun",
		},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, activity := range s.Activities {
			if activity.Kind == domain.ActivityKindUserInput && activity.Status == domain.ActivityStatusPending {
				return true
			}
		}
		return false
	})

	anchored := 0
	for _, activity := range snapshot.Activities {
		if activity.Kind != domain.ActivityKindUserInput && activity.Kind != domain.ActivityKindApproval {
			continue
		}
		if activity.Status != domain.ActivityStatusPending {
			continue
		}
		if activity.TurnID != turn.ID {
			t.Fatalf("pending %s activity stored on turn %q, want the running turn %q",
				activity.Kind, activity.TurnID, turn.ID)
		}
		anchored++
	}
	if anchored != 2 {
		t.Fatalf("anchored pending interactions = %d, want both the question and the approval", anchored)
	}

	// The provider's own tag still wins when it sends one: nothing may rewrite
	// a correctly linked interaction.
	linked := ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, ProviderTurnID: turn.ProviderTurnID,
		RequestID: "tagged-elicitation", Summary: "Follow-up",
		Input: &ports.ChatInputRequest{
			Mode: ports.ChatInputModeForm, Message: "Follow-up",
			Schema: map[string]any{"type": "object"},
		},
	}
	h.conv.emit(linked)
	snapshot = h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, activity := range s.Activities {
			if activity.RequestID == "tagged-elicitation" {
				return true
			}
		}
		return false
	})
	for _, activity := range snapshot.Activities {
		if activity.RequestID != "tagged-elicitation" {
			continue
		}
		if activity.TurnID != turn.ID {
			t.Fatalf("provider-tagged question stored on turn %q, want %q", activity.TurnID, turn.ID)
		}
	}
}
