package chat

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeReplayRecognizesCombinedProviderInput(t *testing.T) {
	turns := []domain.ConversationTurn{{
		ID: "leader", ProviderTurnID: "provider-1", State: domain.TurnStateCompleted,
		ProviderInputText: "combined provider input",
	}}
	messages := []domain.ConversationMessage{{
		TurnID: "leader", Role: domain.MessageRoleUser, Text: "first original message",
	}}
	events := []ports.ChatEvent{{
		Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "provider-1",
		ProviderItemID: "native-batch", Text: "combined provider input",
	}}
	if replay := reconcileNativeHistory(events, turns, messages, nil); len(replay) != 0 {
		t.Fatalf("native replay duplicated the combined provider input: %+v", replay)
	}
}

func TestNativeReplayRecognizesSteeredBatchAfterDrainedBatch(t *testing.T) {
	turns := []domain.ConversationTurn{{
		ID: "leader", ProviderTurnID: "provider-1", State: domain.TurnStateCompleted,
		ProviderInputText: "initial combined input",
	}}
	messages := []domain.ConversationMessage{{
		TurnID: "leader", Role: domain.MessageRoleUser, Text: "first original message",
	}}
	activities := []domain.ConversationActivity{{
		TurnID: "leader", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
		Detail: []byte(`{"event":"steer","providerInputText":"steered combined input","providerInputStoredOnTurn":false}`),
	}}
	events := []ports.ChatEvent{
		{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "provider-1", ProviderItemID: "native-initial", Text: "initial combined input"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "provider-1", ProviderItemID: "native-steer", Text: "steered combined input"},
	}
	if replay := reconcileNativeHistory(events, turns, messages, activities); len(replay) != 0 {
		t.Fatalf("native replay duplicated a combined input: %+v", replay)
	}
}
