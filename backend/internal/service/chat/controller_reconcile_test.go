package chat

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReconcileNativeHistoryUsesLegacyProviderItemAlias(t *testing.T) {
	events := []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "new-scoped-turn"},
		{
			Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "new-scoped-turn",
			ProviderItemID: "new-scoped-assistant", ProviderItemAliases: []string{"legacy-assistant"},
			Text: "answer without a user prompt",
		},
		{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "new-scoped-turn",
			ProviderItemID: "new-scoped-tool", ProviderItemAliases: []string{"legacy-tool"},
			ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusCompleted,
			Summary: "go test ./...", Detail: []byte(`{"parentProviderItemId":"new-scoped-parent"}`),
		},
		{
			Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "new-scoped-turn",
			TurnState: domain.TurnStateCompleted,
		},
	}
	existingTurns := []domain.ConversationTurn{{
		ID: "legacy-turn", ProviderTurnID: "legacy-history-turn", State: domain.TurnStateCompleted,
	}}
	existingMessages := []domain.ConversationMessage{{
		TurnID: "legacy-turn", Role: domain.MessageRoleAssistant,
		ProviderItemID: "legacy-assistant", Text: "answer without a user prompt",
	}}
	existingActivities := []domain.ConversationActivity{{
		TurnID: "legacy-turn", Kind: domain.ActivityKindCommand,
		Status: domain.ActivityStatusCompleted, ProviderItemID: "legacy-tool", Summary: "go test ./...",
		Detail: []byte(`{"parentProviderItemId":"legacy-parent"}`),
	}}

	got := reconcileNativeHistory(events, existingTurns, existingMessages, existingActivities)
	if len(got) != 2 {
		t.Fatalf("reconciled events = %#v, want only turn boundaries", got)
	}
	for _, event := range got {
		if event.ProviderTurnID != "legacy-history-turn" {
			t.Fatalf("reconciled event turn = %q, want legacy-history-turn", event.ProviderTurnID)
		}
	}
}

func TestReconcileNativeHistoryUsesLegacyAliasForAttachmentOnlyUserTurn(t *testing.T) {
	events := []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "new-scoped-turn"},
		{
			Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "new-scoped-turn",
			ProviderItemID: "new-scoped-user", ProviderItemAliases: []string{"legacy-client"},
			ClientMessageID: "new-scoped-user", Text: "[Image]",
		},
		{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "new-scoped-turn", TurnState: domain.TurnStateInterrupted},
	}
	existingTurns := []domain.ConversationTurn{{
		ID: "legacy-turn", ProviderTurnID: "legacy-history-turn", State: domain.TurnStateInterrupted,
	}}
	existingMessages := []domain.ConversationMessage{{
		TurnID: "legacy-turn", Role: domain.MessageRoleUser,
		ClientMessageID: "legacy-client", Text: "",
	}}

	got := reconcileNativeHistory(events, existingTurns, existingMessages, nil)
	for _, event := range got {
		if event.ProviderTurnID != "legacy-history-turn" {
			t.Fatalf("reconciled event turn = %q, want legacy-history-turn; events=%#v",
				event.ProviderTurnID, got)
		}
	}
}

// Hermes' session/load replay carries id-less chunks, stripped reply text, and
// the failed-turn boundary row on interrupted turns, under turn ids the live
// projection never used. The stored completed turn must absorb the stripped
// reply (boundary whitespace differs) without a duplicate, and the interrupted
// turn must not gain a replayed reply on top.
func TestReconcileNativeHistoryAbsorbsHermesReplayShape(t *testing.T) {
	const completedReply = "Answer one.\n"
	const interruptedPrompt = "Do turn two."
	events := []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "acp-history-turn:replay-1"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "acp-history-turn:replay-1",
			ClientMessageID: "user-1", Text: "What is X?"},
		{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "acp-history-turn:replay-1",
			ProviderItemID: "assistant-1", Text: "Answer one."},
		{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "acp-history-turn:replay-1",
			TurnState: domain.TurnStateRecovered},
		{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "acp-history-turn:replay-2"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "acp-history-turn:replay-2",
			ClientMessageID: "user-2", Text: interruptedPrompt},
		{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "acp-history-turn:replay-2",
			TurnState: domain.TurnStateRecovered},
	}
	existingTurns := []domain.ConversationTurn{
		{ID: "ao-turn-1", ProviderTurnID: "live-turn-1", State: domain.TurnStateCompleted},
		{ID: "ao-turn-2", ProviderTurnID: "live-turn-2", State: domain.TurnStateInterrupted},
	}
	existingMessages := []domain.ConversationMessage{
		{TurnID: "ao-turn-1", Role: domain.MessageRoleUser, ClientMessageID: "live-client-1", Text: "What is X?"},
		{TurnID: "ao-turn-1", Role: domain.MessageRoleAssistant, ProviderItemID: "live-assistant-1", Text: completedReply},
		{TurnID: "ao-turn-2", Role: domain.MessageRoleUser, ClientMessageID: "live-client-2", Text: interruptedPrompt},
	}

	got := reconcileNativeHistory(events, existingTurns, existingMessages, nil)
	byTurn := map[string][]ports.ChatEvent{}
	for _, event := range got {
		byTurn[event.ProviderTurnID] = append(byTurn[event.ProviderTurnID], event)
	}
	if len(byTurn["live-turn-1"]) != 2 {
		t.Fatalf("completed turn kept %d events, want only the boundaries: %#v",
			len(byTurn["live-turn-1"]), byTurn["live-turn-1"])
	}
	for _, event := range byTurn["live-turn-1"] {
		if event.Kind == ports.ChatEventTurnCompleted && event.TurnState != domain.TurnStateCompleted {
			t.Fatalf("recovered completion not upgraded to AO's known state: %#v", event)
		}
	}
	// The interrupted turn keeps its user prompt, its recovered completion is
	// corrected, and no replayed reply is added on top.
	if len(byTurn["live-turn-2"]) != 2 {
		t.Fatalf("interrupted turn kept %d events, want only the boundaries: %#v",
			len(byTurn["live-turn-2"]), byTurn["live-turn-2"])
	}
	for _, event := range byTurn["live-turn-2"] {
		if event.Kind == ports.ChatEventTurnCompleted && event.TurnState != domain.TurnStateInterrupted {
			t.Fatalf("interrupted completion rewritten to %q", event.TurnState)
		}
	}
	if _, extra := byTurn["acp-history-turn:replay-2"]; extra {
		t.Fatal("replay turn imported as a duplicate of the interrupted turn")
	}
}
