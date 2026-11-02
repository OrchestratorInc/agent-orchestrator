package chat

import (
	"slices"
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

func TestReconcileNativeHistoryDropsReplayedReasoningUnderNewItemID(t *testing.T) {
	// Hermes tags live thought chunks with a message id but replays them id-less,
	// so the replay mints a new item id and settles the thought as recovered.
	// Live, the same thought completed.
	replay := func(texts ...string) []ports.ChatEvent {
		events := []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "acp-history-turn:s:user-2"},
			{
				Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "acp-history-turn:s:user-2",
				ProviderItemID: "user-2", Text: "Reply with ready.",
			},
		}
		for i, text := range texts {
			events = append(events, ports.ChatEvent{
				Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "acp-history-turn:s:user-2",
				ProviderItemID: "thought-acp-history-turn:s:user-2-" + string(rune('a'+i)),
				ActivityKind:   domain.ActivityKindReasoning, ActivityStatus: domain.ActivityStatusRecovered,
				Summary: "Reasoning", Text: text,
			})
		}
		return append(events, ports.ChatEvent{
			Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "acp-history-turn:s:user-2",
			TurnState: domain.TurnStateCompleted,
		})
	}
	existingTurns := []domain.ConversationTurn{{
		ID: "turn-1", ProviderTurnID: "live-turn", State: domain.TurnStateCompleted,
	}}
	existingMessages := []domain.ConversationMessage{{
		TurnID: "turn-1", Role: domain.MessageRoleUser, Text: "Reply with ready.",
	}}
	existingActivities := []domain.ConversationActivity{{
		TurnID: "turn-1", Kind: domain.ActivityKindReasoning, Status: domain.ActivityStatusCompleted,
		ProviderItemID: "live-thought-uuid", Summary: "Reasoning",
		StreamedText: "**Defining the Core Task**\n\nReady.\n\n\n",
	}}
	reasoning := func(events []ports.ChatEvent) []string {
		var texts []string
		for _, event := range events {
			if event.ActivityKind == domain.ActivityKindReasoning {
				texts = append(texts, event.Text)
			}
		}
		return texts
	}

	for _, tc := range []struct {
		name   string
		replay []string
		want   []string
	}{
		{name: "the stored thought is dropped", replay: []string{"**Defining the Core Task**\n\nReady."}},
		{
			name:   "a different thought is still imported",
			replay: []string{"**Defining the Core Task**\n\nReady.", "A second thought."},
			want:   []string{"A second thought."},
		},
		{
			name:   "a repeated thought keeps its count",
			replay: []string{"**Defining the Core Task**\n\nReady.", "**Defining the Core Task**\n\nReady."},
			want:   []string{"**Defining the Core Task**\n\nReady."},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := reconcileNativeHistory(replay(tc.replay...), existingTurns, existingMessages, existingActivities)
			if texts := reasoning(got); !slices.Equal(texts, tc.want) {
				t.Fatalf("reconciled reasoning = %q, want %q", texts, tc.want)
			}
		})
	}
}
