package chat_test

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type batchAcceptanceStore struct {
	chatsvc.Store
	mode string
}

func (s *batchAcceptanceStore) BindTurnToProvider(ctx context.Context, turnID, providerID string, now time.Time) error {
	if providerID == "provider-turn-2" && s.mode == "bind_failure" {
		return syscall.ENOSPC
	}
	if err := s.Store.BindTurnToProvider(ctx, turnID, providerID, now); err != nil {
		return err
	}
	if providerID == "provider-turn-2" && s.mode == "live_reconnect" {
		turn, err := s.TurnByID(ctx, turnID)
		if err != nil {
			return err
		}
		return s.FailReservedQueuedBatch(ctx, turn.ConversationID, now)
	}
	return nil
}

func (s *batchAcceptanceStore) CompleteQueuedBatch(context.Context, string, []string, string, string, time.Time) error {
	return syscall.ENOSPC
}

type failedBatchAcceptanceStore struct{ chatsvc.Store }

func (*failedBatchAcceptanceStore) MarkQueuedBatchStarted(_ context.Context, _ string, turnIDs []string, _ time.Time) error {
	if len(turnIDs) == 0 {
		return nil
	}
	return syscall.ENOSPC
}

func TestBatchFollowerRetainsProviderAcceptance(t *testing.T) {
	for _, mode := range []string{"completion_failure", "live_reconnect", "bind_failure", "acceptance_write_failure", "never_sent", "deferred_prompt_rejected", "deferred_never_sent"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			provider := newFakeConversation()
			var conv ports.ChatConversation = provider
			if mode == "deferred_prompt_rejected" || mode == "deferred_never_sent" {
				conv = &deferredConversation{fakeConversation: provider, start: func(id string) error {
					if id != "provider-turn-2" {
						return nil
					}
					if mode == "deferred_never_sent" {
						return errors.New("prompt never started")
					}
					provider.emit(
						ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: id},
						ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: id,
							TurnState: domain.TurnStateFailed, Err: errors.New("provider session missing")},
					)
					return nil
				}}
			}
			h := newHarnessWithConversationAndStore(t, conv, func(st *store.Store) chatsvc.Store {
				var wrapped chatsvc.Store = &batchAcceptanceStore{Store: st, mode: mode}
				if mode == "acceptance_write_failure" {
					wrapped = &failedBatchAcceptanceStore{Store: wrapped}
				}
				return wrapped
			})
			for _, text := range []string{"active", "leader", "review-done:run-1:0"} {
				if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: text, ClientMessageID: text, Origin: domain.MessageOriginAutomation}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "never_sent" {
				provider.mu.Lock()
				provider.sendErr = errors.New("provider unavailable")
				provider.mu.Unlock()
			}
			provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				states := turnStateByText(t, s)
				return states["review-done:run-1:0"] == domain.TurnStateFailed &&
					(mode != "deferred_prompt_rejected" || states["leader"] == domain.TurnStateFailed)
			})
			state, _, started, found, err := h.st.ReviewNoticeChatDelivery(ctx, testSession, "review-done:run-1:0")
			want := mode != "never_sent" && mode != "deferred_never_sent" && mode != "deferred_prompt_rejected"
			if err != nil || !found || state != domain.TurnStateFailed || started != want {
				t.Fatalf("follower: state=%s started=%v, want failed started=%v; found=%v err=%v", state, started, want, found, err)
			}
		})
	}
}
