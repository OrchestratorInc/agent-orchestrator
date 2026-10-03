package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestAsyncQuestionAnswerSurvivesCompletedTurnAndUsesMessageIntake(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	turn, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "Ask me"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := ports.ChatQuestionForm([]ports.ChatQuestion{{ID: "how", Prompt: "How are you?", Custom: true, Options: []ports.ChatQuestionOption{{Value: "Fine", Label: "Fine"}}}})
	if err != nil {
		t.Fatal(err)
	}
	input.ResponseMode = "message"
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventInputRequested, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: "question", RequestID: "async:question", Input: &input}, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Activities) == 1 && len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
	})
	// Detached RPC cleanup must not invalidate a message-delivered question.
	if err := h.st.FailPendingInputs(ctx, h.ctrl.ConversationID(), h.now()); err != nil {
		t.Fatal(err)
	}
	if err := h.ctrl.ResolveInput(ctx, "async:question", ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: map[string]any{"question_0": "invented"}}); !errors.Is(err, ports.ErrChatDecisionNotOffered) {
		t.Fatalf("invalid answer: %v", err)
	}
	if err := h.ctrl.ResolveInput(ctx, "async:question", ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: map[string]any{"question_0": "Fine"}}); err != nil {
		t.Fatal(err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) == 2 && s.Activities[0].Status == domain.ActivityStatusResolved
	})
	if snapshot.Messages[1].Text != "How are you?\nFine" || snapshot.Messages[1].ClientMessageID != "input-answer:async:question" {
		t.Fatalf("answer: %+v", snapshot.Messages[1])
	}
	if err := h.ctrl.ResolveInput(ctx, "async:question", ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: map[string]any{"question_0": "Fine"}}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("duplicate answer should not dispatch: %v", err)
	}
}

func TestAsyncQuestionCancelDoesNotSendAMessage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	turn, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "Ask"})
	if err != nil {
		t.Fatal(err)
	}
	input, _ := ports.ChatQuestionForm([]ports.ChatQuestion{{ID: "how", Prompt: "How?"}})
	input.ResponseMode = "message"
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventInputRequested, ProviderTurnID: turn.ProviderTurnID, RequestID: "async:cancel", ProviderItemID: "cancel", Input: &input})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Activities) == 1 })
	if err := h.ctrl.ResolveInput(ctx, "async:cancel", ports.ChatInputResponse{Action: ports.ChatInputActionCancel}); err != nil {
		t.Fatal(err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Activities[0].Status == domain.ActivityStatusResolved
	})
	if len(snapshot.Messages) != 1 {
		t.Fatalf("cancel sent message: %+v", snapshot.Messages)
	}
}

func TestAsyncAnswerQueuesWhileAgentContinues(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	turn, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "Ask"})
	if err != nil {
		t.Fatal(err)
	}
	input, _ := ports.ChatQuestionForm([]ports.ChatQuestion{{ID: "how", Prompt: "How?"}})
	input.ResponseMode = "message"
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventInputRequested, ProviderTurnID: turn.ProviderTurnID, RequestID: "async:queued", ProviderItemID: "queued", Input: &input})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Activities) == 1 })
	if blocking, err := h.st.HasPendingConversationInteractions(ctx, h.ctrl.ConversationID()); err != nil || blocking {
		t.Fatalf("async request blocks provider: %v %v", blocking, err)
	}
	if err := h.ctrl.ResolveInput(ctx, "async:queued", ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: map[string]any{"question_0": "Fine"}}); err != nil {
		t.Fatal(err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Turns) == 2 })
	if snapshot.Turns[1].State != domain.TurnStateQueued {
		t.Fatalf("answer not queued: %+v", snapshot.Turns)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return s.Turns[1].State == domain.TurnStateRunning })
}
