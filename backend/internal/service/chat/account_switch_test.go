package chat_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func switchNotices(snapshot store.ConversationSnapshot) map[string]int {
	notices := map[string]int{}
	for _, activity := range snapshot.Activities {
		var detail map[string]string
		_ = json.Unmarshal(activity.Detail, &detail)
		if detail["event"] == chatsvc.AccountSwitchedEvent {
			notices[activity.TurnID]++
		}
	}
	return notices
}

func startRunningTurn(t *testing.T, h *harness, text, providerTurnID string) domain.ConversationTurn {
	t.Helper()
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: text, ClientMessageID: "c-" + providerTurnID}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: providerTurnID})
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)[text] == domain.TurnStateRunning
	})
	for _, turn := range snapshot.Turns {
		if turn.ProviderTurnID == providerTurnID {
			return turn
		}
	}
	t.Fatalf("turn %s not found", providerTurnID)
	return domain.ConversationTurn{}
}

func TestMarkAccountSwitchTurnsTheStoppedTurnIntoASwitch(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	stopped := startRunningTurn(t, h, "long work", "provider-turn-1")
	if err := h.svc.PrepareChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionInterrupt); err != nil {
		t.Fatalf("prepare handoff: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["long work"] == domain.TurnStateFailed
	})

	if err := h.svc.MarkAccountSwitch(ctx, testSession, "acct-b"); err != nil {
		t.Fatalf("MarkAccountSwitch: %v", err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["long work"] == domain.TurnStateInterrupted
	})
	for _, turn := range snapshot.Turns {
		if turn.ID == stopped.ID && turn.ErrorMessage != "" {
			t.Fatalf("stopped turn kept an error: %q", turn.ErrorMessage)
		}
	}
	if got := switchNotices(snapshot); got[stopped.ID] != 1 || len(got) != 1 {
		t.Fatalf("notices = %v, want one on the stopped turn", got)
	}
	if err := h.svc.MarkAccountSwitch(ctx, testSession, "acct-b"); err != nil {
		t.Fatal(err)
	}
	if got := switchNotices(h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return true })); got[stopped.ID] != 1 {
		t.Fatalf("notices after a repeat = %v", got)
	}
}

func TestMarkAccountSwitchMarksTheTurnTheHandoffStopped(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	stopped := startRunningTurn(t, h, "long work", "provider-turn-1")
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "child-turn", ProviderConversationID: "child-thread"})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "child-turn", ProviderConversationID: "child-thread", TurnState: domain.TurnStateCompleted})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "queued", ClientMessageID: "c-queued"}); err != nil {
		t.Fatalf("queue a message: %v", err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Turns) == 3 })
	if err := h.svc.PrepareChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionInterrupt); err != nil {
		t.Fatalf("prepare handoff: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["long work"] == domain.TurnStateFailed
	})

	if err := h.svc.MarkAccountSwitch(ctx, testSession, "acct-b"); err != nil {
		t.Fatalf("MarkAccountSwitch: %v", err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["long work"] == domain.TurnStateInterrupted
	})
	if got := switchNotices(snapshot); got[stopped.ID] != 1 || len(got) != 1 {
		t.Fatalf("notices = %v, want one on the stopped turn %s", got, stopped.ID)
	}
}

func TestMarkAccountSwitchWithoutAStoppedTurnAddsNothing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	startRunningTurn(t, h, "short work", "provider-turn-1")
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["short work"] == domain.TurnStateCompleted
	})
	if err := h.svc.PrepareChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionDrain); err != nil {
		t.Fatalf("prepare handoff: %v", err)
	}
	if err := h.svc.MarkAccountSwitch(ctx, testSession, "acct-b"); err != nil {
		t.Fatal(err)
	}
	snapshot := h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return true })
	if got := switchNotices(snapshot); len(got) != 0 || turnStateByText(t, snapshot)["short work"] != domain.TurnStateCompleted {
		t.Fatalf("notices = %v, state %s", got, turnStateByText(t, snapshot)["short work"])
	}
}
