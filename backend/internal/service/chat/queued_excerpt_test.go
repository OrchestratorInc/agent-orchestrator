package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// Keep queue writes and settlement real while simulating source changes after
// the excerpt has been accepted. The invalid case simulates a malformed stored
// reference, which must use the same terminal queue handling as stale sources.
type changedExcerptStore struct {
	chatsvc.Store
	mode    string
	changed atomic.Bool
}

func (s *changedExcerptStore) ConversationMessages(ctx context.Context, id string) ([]domain.ConversationMessage, error) {
	messages, err := s.Store.ConversationMessages(ctx, id)
	if err != nil || !s.changed.Load() {
		return messages, err
	}
	for i, message := range messages {
		if message.Text != "Selected source text." {
			continue
		}
		switch s.mode {
		case "revision":
			messages[i].Revision++
		case "missing pair":
			for j := range messages {
				if messages[j].TurnID == message.TurnID && messages[j].Role == domain.MessageRoleUser {
					messages[j].Text = ""
				}
			}
		}
	}
	return messages, nil
}

func (s *changedExcerptStore) TurnByID(ctx context.Context, id string) (domain.ConversationTurn, error) {
	turn, err := s.Store.TurnByID(ctx, id)
	if err == nil && s.changed.Load() && s.mode == "rollback" {
		when := turn.RequestedAt
		turn.RolledBackAt = &when
	}
	return turn, err
}

func (s *changedExcerptStore) NextQueuedTurn(ctx context.Context, id string) (domain.QueuedTurn, error) {
	queued, err := s.Store.NextQueuedTurn(ctx, id)
	if err != nil || !s.changed.Load() || s.mode != "invalid" || queued.DeliveryContentJSON == "" {
		return queued, err
	}
	var content []ports.ChatContent
	if err := json.Unmarshal([]byte(queued.DeliveryContentJSON), &content); err != nil {
		return queued, err
	}
	for i := range content {
		if content[i].Excerpt != nil {
			content[i].Excerpt.Reference.ConversationID = "another-conversation"
		}
	}
	encoded, err := json.Marshal(content)
	queued.DeliveryContentJSON = string(encoded)
	return queued, err
}

func TestQueuedExcerptValidationFailureContinuesDrain(t *testing.T) {
	for _, mode := range []string{"revision", "rollback", "missing pair", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			var changed *changedExcerptStore
			h := newHarnessWithConversationAndStore(t, nil, func(st *sqlite.Store) chatsvc.Store {
				changed = &changedExcerptStore{Store: st, mode: mode}
				return changed
			})
			ctx := context.Background()
			send := func(text string, refs ...ports.ChatExcerptReference) domain.ConversationTurn {
				t.Helper()
				turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
					Text: text, ClientMessageID: text, Origin: domain.MessageOriginHuman, Excerpts: refs,
				})
				if err != nil {
					t.Fatal(err)
				}
				return turn
			}
			seed := send("seed")
			h.conv.emit(
				ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: seed.ProviderTurnID},
				ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: seed.ProviderTurnID,
					ProviderItemID: "source", Text: "Selected source text."},
				ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: seed.ProviderTurnID,
					TurnState: domain.TurnStateCompleted},
			)
			snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return len(s.Messages) == 2 && s.Turns[0].State == domain.TurnStateCompleted
			})
			source := snapshot.Messages[1]
			active := send("active")
			stale := send("stale excerpt", ports.ChatExcerptReference{
				ConversationID: h.ctrl.ConversationID(), MessageID: source.ID,
				Revision: source.Revision, Text: "Selected source",
			})
			if stale.State != domain.TurnStateQueued {
				t.Fatalf("excerpt state = %s, want queued", stale.State)
			}
			send("next valid")
			send("last valid")
			changed.changed.Store(true)
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
				ProviderTurnID: active.ProviderTurnID, TurnState: domain.TurnStateCompleted})
			snapshot = h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				states := turnStateByText(t, s)
				return states["stale excerpt"] == domain.TurnStateFailed && states["next valid"] == domain.TurnStateRunning
			})
			if turnStateByText(t, snapshot)["last valid"] != domain.TurnStateQueued {
				t.Fatal("drain dispatched more than one valid turn")
			}
			for _, turn := range snapshot.Turns {
				if turn.ID == stale.ID && turn.ErrorMessage != "chat excerpt is stale" {
					t.Fatalf("failure reason = %q", turn.ErrorMessage)
				}
			}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
				ProviderTurnID: "provider-turn-3", TurnState: domain.TurnStateCompleted})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["last valid"] == domain.TurnStateRunning
			})
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
				ProviderTurnID: "provider-turn-4", TurnState: domain.TurnStateCompleted})
			snapshot = h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["last valid"] == domain.TurnStateCompleted
			})
			if turnStateByText(t, snapshot)["stale excerpt"] != domain.TurnStateFailed {
				t.Fatal("later drains retried the stale turn")
			}
			if got := h.conv.sentTexts(); !reflect.DeepEqual(got, []string{"seed", "active", "next valid", "last valid"}) {
				t.Fatalf("provider sends = %v", got)
			}
		})
	}
}

func TestQueuedProviderDispatchFailureStopsDrain(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, text := range []string{"active", "provider failure", "still queued"} {
		if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: text, ClientMessageID: text, Origin: domain.MessageOriginHuman,
		}); err != nil {
			t.Fatal(err)
		}
	}
	h.conv.mu.Lock()
	h.conv.sendErr = errors.New("provider unavailable")
	h.conv.mu.Unlock()
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
		ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["provider failure"] == domain.TurnStateFailed
	})
	if turnStateByText(t, snapshot)["still queued"] != domain.TurnStateQueued {
		t.Fatal("provider failure settled an unrelated queued message")
	}
	if got := h.conv.sendCallCount(); got != 2 {
		t.Fatalf("provider calls = %d, want initial send and one failed dispatch", got)
	}
}
