package chat_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type scopedLiveConversation struct {
	*liveReconnectedConversation
	childTurnID string
}

func (c *scopedLiveConversation) IsChildProviderTurn(id string) bool { return id == c.childTurnID }

func TestLiveReconnectDoesNotRestoreChildTurnAsRootOwnership(t *testing.T) {
	for _, tc := range []struct {
		name            string
		rootCompleted   bool
		startedObserved bool
	}{
		{"root still running", false, true},
		{"root already completed", true, true},
		{"bound root before native started notification", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			firstProvider := &terminatingConversation{fakeConversation: newFakeConversation()}
			h := newHarnessWithConversation(t, firstProvider)
			root, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "root"})
			if err != nil {
				t.Fatal(err)
			}
			if tc.startedObserved {
				firstProvider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: root.ProviderTurnID})
			}
			h.advance(time.Second)
			firstProvider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "child-turn", ProviderConversationID: "child-thread"})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Turns) == 2 })
			if tc.rootCompleted {
				firstProvider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: root.ProviderTurnID, TurnState: domain.TurnStateCompleted})
				h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return s.Turns[0].State == domain.TurnStateCompleted })
			}
			h.svc.StopAll(ctx)
			h.ctrl.Wait()

			provider := &scopedLiveConversation{
				liveReconnectedConversation: &liveReconnectedConversation{nativeHistoryConversation: &nativeHistoryConversation{fakeConversation: newFakeConversation()}},
				childTurnID:                 "child-turn",
			}
			provider.turnSeq = 1
			var ids atomic.Int64
			svc := chatsvc.New(chatsvc.Options{
				Store: h.st, Sessions: h.st, Reader: fullSnapshotReader(h.st),
				Drivers: fakeRegistry{driver: fakeDriver{conv: provider}},
				Log:     slog.New(slog.DiscardHandler),
				NewID:   func() string { return fmt.Sprintf("scope-restart-%d", ids.Add(1)) },
				Now:     h.now,
			})
			t.Cleanup(func() { svc.StopAll(ctx) })
			controller, err := svc.Start(ctx, chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
				WorkspacePath: t.TempDir(), ProviderConversationID: firstProvider.ProviderConversationID(),
			})
			if err != nil {
				t.Fatal(err)
			}
			state, nextState := ports.ChatControllerBusy, domain.TurnStateQueued
			if tc.rootCompleted {
				state, nextState = ports.ChatControllerReady, domain.TurnStateRunning
			}
			if controller.State() != state {
				t.Fatalf("controller restored child ownership: state=%s, want %s", controller.State(), state)
			}
			next, err := controller.Send(ctx, ports.ChatUserMessage{Text: "next root turn"})
			if err != nil || next.State != nextState {
				t.Fatalf("next root turn: %+v, %v; want %s", next, err, nextState)
			}
			if !tc.rootCompleted {
				provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: root.ProviderTurnID, TurnState: domain.TurnStateCompleted})
				h.ctrl = controller
				h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
					return turnStateByText(t, s)["next root turn"] == domain.TurnStateRunning
				})
			}
		})
	}
}
