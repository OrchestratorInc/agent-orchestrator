package chat

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLiveTurnHistoryRequiresMutuallyUniqueStreamedPrefix(t *testing.T) {
	for _, tc := range []struct {
		name              string
		pending, native   int
		exact, otherTurn  bool
		firstEqualsPrefix bool
		wantFirstItem     string
	}{
		{name: "one native item and one pending prefix", pending: 1, native: 1, wantFirstItem: "live-1"},
		{name: "two pending prefixes are ambiguous", pending: 2, native: 1, wantFirstItem: "native-1"},
		{name: "two native items are ambiguous", pending: 1, native: 2, wantFirstItem: "native-1"},
		{name: "streamed text is not proof of an earlier completed item", pending: 1, native: 2, firstEqualsPrefix: true, wantFirstItem: "native-1"},
		{name: "exact identity wins over repeated prefixes", pending: 2, native: 1, exact: true, wantFirstItem: "live-1"},
		{name: "other turns cannot claim a pending item", pending: 1, native: 1, otherTurn: true, wantFirstItem: "native-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existing := ConversationRows{Turns: []domain.ConversationTurn{{ID: "ao-turn", ProviderTurnID: "pending", State: domain.TurnStateRunning}}}
			for _, id := range []string{"live-1", "live-2"}[:tc.pending] {
				existing.Messages = append(existing.Messages, domain.ConversationMessage{
					TurnID: "ao-turn", ProviderItemID: id, Role: domain.MessageRoleAssistant, Text: "Hello", Streaming: true,
				})
			}
			var events []ports.ChatEvent
			for _, id := range []string{"native-1", "native-2"}[:tc.native] {
				events = append(events, ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "pending", ProviderItemID: id, Text: "Hello complete"})
			}
			if tc.exact {
				events[0].ProviderItemID = "live-1"
			}
			if tc.firstEqualsPrefix {
				events[0].Text = "Hello"
			}
			if tc.otherTurn {
				events[0].ProviderTurnID = "another-turn"
			}
			events = append(events, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "pending", TurnState: domain.TurnStateCompleted})
			got := reconcileLiveTurnHistory("pending", existing, events)
			if len(got) != len(events) || got[0].ProviderItemID != tc.wantFirstItem {
				t.Fatalf("reconciled events = %+v, want first item %q and %d events", got, tc.wantFirstItem, len(events))
			}
			if last := got[len(got)-1]; last.Kind != ports.ChatEventTurnCompleted || last.ProviderTurnID != "pending" || last.TurnState != domain.TurnStateCompleted {
				t.Fatalf("reconciliation changed terminal proof: %+v", last)
			}
			if tc.native == 2 && got[1].ProviderItemID != "native-2" {
				t.Fatalf("ambiguous later native item claimed a pending prefix: %+v", got[1])
			}
		})
	}
}
