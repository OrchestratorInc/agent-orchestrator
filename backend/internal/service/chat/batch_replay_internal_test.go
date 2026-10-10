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
