package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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

func (s *changedExcerptStore) ListQueuedBatch(ctx context.Context, id string, limit int) ([]domain.QueuedTurn, error) {
	batch, err := s.Store.ListQueuedBatch(ctx, id, limit)
	if err != nil || !s.changed.Load() || s.mode != "invalid" {
		return batch, err
	}
	for j := range batch {
		if batch[j].DeliveryContentJSON == "" {
			continue
		}
		var content []ports.ChatContent
		if err := json.Unmarshal([]byte(batch[j].DeliveryContentJSON), &content); err != nil {
			return batch, err
		}
		for i := range content {
			if content[i].Excerpt != nil {
				content[i].Excerpt.Reference.ConversationID = "another-conversation"
			}
		}
		encoded, err := json.Marshal(content)
		batch[j].DeliveryContentJSON = string(encoded)
		if err != nil {
			return batch, err
		}
	}
	return batch, nil
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
			last := send("last valid")
			changed.changed.Store(true)
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
				ProviderTurnID: active.ProviderTurnID, TurnState: domain.TurnStateCompleted})
			snapshot = h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				states := turnStateByText(t, s)
				return states["stale excerpt"] == domain.TurnStateFailed && states["next valid"] == domain.TurnStateRunning
			})
			lastTurn, err := h.st.TurnByID(ctx, last.ID)
			if err != nil || lastTurn.State != domain.TurnStateCompleted {
				t.Fatalf("last queued message did not join the batch: turn=%+v err=%v", lastTurn, err)
			}
			for _, turn := range snapshot.Turns {
				if turn.ID == stale.ID && !strings.Contains(turn.ErrorMessage, "chat excerpt") {
					t.Fatalf("failure reason = %q", turn.ErrorMessage)
				}
			}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
				ProviderTurnID: "provider-turn-3", TurnState: domain.TurnStateCompleted})
			snapshot = h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["next valid"] == domain.TurnStateCompleted
			})
			if turnStateByText(t, snapshot)["stale excerpt"] != domain.TurnStateFailed {
				t.Fatal("later drains retried the stale turn")
			}
			if got := h.conv.sentTexts(); len(got) != 3 ||
				!reflect.DeepEqual([]string{parseDeliveredBatch(t, got[2])[0].Text, parseDeliveredBatch(t, got[2])[1].Text}, []string{"next valid", "last valid"}) {
				t.Fatalf("provider sends = %v", got)
			}
		})
	}
}

func TestQueuedProviderDispatchFailureFailsClaimedBatchOnce(t *testing.T) {
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
	if turnStateByText(t, snapshot)["still queued"] != domain.TurnStateFailed {
		t.Fatal("provider failure did not preserve the second batch member as failed")
	}
	if got := h.conv.sendCallCount(); got != 2 {
		t.Fatalf("provider calls = %d, want initial send and one failed dispatch", got)
	}
}
