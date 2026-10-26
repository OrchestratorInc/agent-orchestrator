package chat_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// Hold a late run before its transaction starts: reconciliation must be able to
// commit while this delta is waiting, just as it can while the pump is batching.
type stoppedDeltaStore struct {
	chatsvc.Store
	observed chan struct{}
	release  chan struct{}
	once     sync.Once
	stopOnce sync.Once
}

func (s *stoppedDeltaStore) unblock() { s.stopOnce.Do(func() { close(s.release) }) }

func (s *stoppedDeltaStore) ProjectProviderEvent(
	ctx context.Context, conversationID string, session domain.SessionID,
	generation, providerEventID, method, payloadJSON string, now time.Time,
	project func(context.Context) error,
) (bool, error) {
	if method == string(ports.ChatEventMessageDelta) && strings.Contains(payloadJSON, "during-stop") {
		s.once.Do(func() { close(s.observed) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return s.Store.ProjectProviderEvent(ctx, conversationID, session, generation,
		providerEventID, method, payloadJSON, now, project)
}

type lateDeltaInterruptConversation struct {
	*fakeConversation
	observed <-chan struct{}
}

func (c *lateDeltaInterruptConversation) Interrupt(ctx context.Context, turn string) error {
	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventMessageDelta, ProviderTurnID: turn,
		ProviderItemID: "reply", Delta: " during-stop",
	})
	select {
	case <-c.observed:
		return ports.ErrChatNoActiveTurn
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestInterruptRefusalLateDeltasRemainSettled(t *testing.T) {
	for _, live := range []bool{false, true} {
		name := "stale-durable-turn"
		if live {
			name = "acknowledged-live-turn"
		}
		t.Run(name, func(t *testing.T) {
			gate := &stoppedDeltaStore{observed: make(chan struct{}), release: make(chan struct{})}
			conv := &lateDeltaInterruptConversation{fakeConversation: newFakeConversation(), observed: gate.observed}
			h := newHarnessWithConversationAndStore(t, conv, func(st *sqlite.Store) chatsvc.Store {
				gate.Store = st
				return gate
			})
			t.Cleanup(gate.unblock)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			const providerTurn = "provider-turn-1"
			if live {
				if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "running", ClientMessageID: "running"}); err != nil {
					t.Fatal(err)
				}
				conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: providerTurn})
			} else if err := h.st.AdoptProviderTurn(ctx, h.ctrl.ConversationID(), testSession, h.ctrl.Generation(), "stale", providerTurn, h.now()); err != nil {
				t.Fatal(err)
			}
			conv.emit(ports.ChatEvent{
				Kind: ports.ChatEventMessageDelta, ProviderTurnID: providerTurn,
				ProviderItemID: "reply", Delta: "before",
			})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				for _, message := range s.Messages {
					if message.ProviderItemID == "reply" {
						return message.Text == "before" && message.Streaming
					}
				}
				return false
			})
			if err := h.svc.Interrupt(ctx, testSession); err != nil {
				t.Fatalf("Interrupt: %v", err)
			}
			gate.unblock()
			// These arrive strictly after reconciliation, so adding another flush to
			// Interrupt cannot satisfy the durable terminal-state invariant.
			conv.emit(
				ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: providerTurn, ProviderItemID: "reply", Delta: " after-stop"},
				ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: providerTurn, ProviderItemID: "late-reply", Delta: "late first item"},
			)
			snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				var reply, late bool
				for _, message := range s.Messages {
					reply = reply || (message.ProviderItemID == "reply" && message.Text == "before during-stop after-stop")
					late = late || (message.ProviderItemID == "late-reply" && message.Text == "late first item")
				}
				return reply && late
			})
			if len(snapshot.Turns) != 1 || snapshot.Turns[0].State != domain.TurnStateInterrupted {
				t.Fatalf("Stop terminal state = %+v", snapshot.Turns)
			}
			for _, message := range snapshot.Messages {
				if message.Role == domain.MessageRoleAssistant && message.Streaming {
					t.Errorf("late output reopened stopped reply: %+v", message)
				}
			}
		})
	}
}
