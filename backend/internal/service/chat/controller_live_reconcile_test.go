package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type liveRecoveryConversation struct{ ports.ChatConversation }

func (liveRecoveryConversation) ProviderConversationID() string     { return "root" }
func (liveRecoveryConversation) IsChildProviderTurn(id string) bool { return id == "child" }

type liveRecoveryFencedStore struct{ Store }

func (liveRecoveryFencedStore) ProjectProviderEvent(context.Context, string, domain.SessionID, string, string, string, string, time.Time, func(context.Context) error) (bool, error) {
	return false, nil // A newer controller generation owns this conversation.
}

func TestLiveTurnAdmissionRequiresExactUnambiguousRootFacts(t *testing.T) {
	running := func(id, provider, branch string) domain.ConversationTurn {
		return domain.ConversationTurn{ID: id, ProviderTurnID: provider, BranchID: branch, State: domain.TurnStateRunning}
	}
	for _, tc := range []struct {
		name     string
		turns    []domain.ConversationTurn
		messages []domain.ConversationMessage
		live     []ports.ChatLiveTurn
		want     string
		wantErr  bool
	}{
		{name: "legacy reconnect keeps prior latest durable selection", turns: []domain.ConversationTurn{{ID: "old", ProviderTurnID: "old", State: domain.TurnStateRunning}, {ID: "latest", ProviderTurnID: "latest", State: domain.TurnStateRunning, RequestedAt: time.Unix(1, 0)}}, want: "latest"},
		{name: "provider-owned active work gates sends", live: []ports.ChatLiveTurn{{ProviderTurnID: "native", Active: true}}, want: "native"},
		{name: "completed retained work cannot claim busy ownership", live: []ports.ChatLiveTurn{{ProviderTurnID: "native"}}},
		{name: "matching durable root remains busy", turns: []domain.ConversationTurn{running("ao", "native", "active")}, live: []ports.ChatLiveTurn{{ProviderTurnID: "native", Active: true}}, want: "native"},
		{name: "different active root fails closed", turns: []domain.ConversationTurn{running("ao", "durable", "active")}, live: []ports.ChatLiveTurn{{ProviderTurnID: "different", Active: true}}, wantErr: true},
		{name: "multiple durable roots fail closed", turns: []domain.ConversationTurn{running("one", "one", "active"), running("two", "two", "active")}, live: []ports.ChatLiveTurn{{ProviderTurnID: "one", Active: true}}, wantErr: true},
		{name: "duplicate host root fails closed", live: []ports.ChatLiveTurn{{ProviderTurnID: "native"}, {ProviderTurnID: "native"}}, wantErr: true},
		{name: "multiple active host roots fail closed", live: []ports.ChatLiveTurn{{ProviderTurnID: "one", Active: true}, {ProviderTurnID: "two", Active: true}}, wantErr: true},
		{name: "shared durable client key fails closed", turns: []domain.ConversationTurn{running("one", "", "active"), running("two", "", "active")}, messages: []domain.ConversationMessage{{TurnID: "one", Role: domain.MessageRoleUser, ClientMessageID: "shared"}, {TurnID: "two", Role: domain.MessageRoleUser, ClientMessageID: "shared"}}, live: []ports.ChatLiveTurn{{ProviderTurnID: "native", ClientMessageID: "shared"}}, wantErr: true},
		{name: "one client key accepted twice fails closed", turns: []domain.ConversationTurn{running("ao", "", "active")}, live: []ports.ChatLiveTurn{{ProviderTurnID: "one", ClientMessageID: "ao"}, {ProviderTurnID: "two", ClientMessageID: "ao"}}, wantErr: true},
		{name: "old branch cannot claim current root", turns: []domain.ConversationTurn{running("old", "old-native", "old")}, live: []ports.ChatLiveTurn{{ProviderTurnID: "native", Active: true}}, want: "native"},
		{name: "child cannot claim root", turns: []domain.ConversationTurn{running("child-ao", "child", "active")}, live: []ports.ChatLiveTurn{{ProviderTurnID: "child", Active: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Controller{conv: liveRecoveryConversation{}, conversation: domain.ConversationRecord{ActiveBranchID: "active"}, state: ports.ChatControllerReady}
			rows := ConversationRows{Turns: tc.turns, Messages: tc.messages}
			got, err := c.restoreAcceptedLiveTurns(context.Background(), &rows, tc.live)
			if tc.wantErr {
				if !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
					t.Fatalf("admission error = %v", err)
				}
				if c.pendingTurnID != "" || c.state != ports.ChatControllerReady {
					t.Fatal("ambiguous host facts changed controller ownership")
				}
				return
			}
			if err != nil || got != tc.want || c.pendingTurnID != tc.want || c.dispatchingTurnID != "" {
				t.Fatalf("admitted=%q pending=%q dispatching=%q err=%v", got, c.pendingTurnID, c.dispatchingTurnID, err)
			}
			if (c.state == ports.ChatControllerBusy) != (tc.want != "") {
				t.Fatalf("state=%s admitted=%q", c.state, got)
			}
		})
	}
	// Rollback is an explicit identity boundary even when native work is active.
	now := time.Now()
	c := &Controller{conv: liveRecoveryConversation{}, conversation: domain.ConversationRecord{ActiveBranchID: "active"}, state: ports.ChatControllerReady}
	rows := ConversationRows{Turns: []domain.ConversationTurn{{ID: "rolled-back", ProviderTurnID: "old", BranchID: "active", State: domain.TurnStateRunning, RolledBackAt: &now}}}
	if got, err := c.restoreAcceptedLiveTurns(context.Background(), &rows, []ports.ChatLiveTurn{{ProviderTurnID: "native", Active: true}}); err != nil || got != "native" {
		t.Fatalf("rolled-back work claimed root: %q %v", got, err)
	}
}

func TestAcceptedLiveTurnBindingHonorsControllerGenerationFence(t *testing.T) {
	c := &Controller{conv: liveRecoveryConversation{}, store: liveRecoveryFencedStore{}, now: time.Now,
		conversation: domain.ConversationRecord{ActiveBranchID: "active"}, state: ports.ChatControllerReady}
	rows := ConversationRows{Turns: []domain.ConversationTurn{{ID: "original", State: domain.TurnStateRunning, BranchID: "active"}}}
	_, err := c.restoreAcceptedLiveTurns(context.Background(), &rows, []ports.ChatLiveTurn{{ProviderTurnID: "accepted", ClientMessageID: "original", Active: true}})
	if !errors.Is(err, ports.ErrChatRecoveryInconclusive) || rows.Turns[0].ProviderTurnID != "" || c.dispatchingTurnID != "" || c.pendingTurnID != "" {
		t.Fatalf("fenced binding changed ownership: row=%+v dispatching=%q pending=%q err=%v", rows.Turns[0], c.dispatchingTurnID, c.pendingTurnID, err)
	}
}

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
