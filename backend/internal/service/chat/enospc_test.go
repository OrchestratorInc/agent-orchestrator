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

type enospcOnceStore struct {
	chatsvc.Store
	failed bool
}

func (s *enospcOnceStore) AppendUserMessage(ctx context.Context, conversationID string, session domain.SessionID, generation string, msg domain.ConversationMessage, turnID string, now time.Time) (bool, error) {
	if !s.failed {
		s.failed = true
		return false, syscall.ENOSPC
	}
	return s.Store.AppendUserMessage(ctx, conversationID, session, generation, msg, turnID, now)
}

type enospcBindStore struct {
	chatsvc.Store
	failed bool
}

func (s *enospcBindStore) BindTurnToProvider(ctx context.Context, turnID, providerTurnID string, now time.Time) error {
	if !s.failed {
		s.failed = true
		return syscall.ENOSPC
	}
	return s.Store.BindTurnToProvider(ctx, turnID, providerTurnID, now)
}

func TestSendRetryAfterENOSPCDoesNotDuplicateProviderTurn(t *testing.T) {
	h := newHarnessWithConversationAndStore(t, nil, func(st *store.Store) chatsvc.Store {
		return &enospcOnceStore{Store: st}
	})
	ctx := context.Background()
	msg := ports.ChatUserMessage{Text: "inspect this", ClientMessageID: "enospc-retry", Origin: domain.MessageOriginHuman}

	if _, err := h.svc.Send(ctx, testSession, msg); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("first Send error = %v, want ENOSPC", err)
	}
	if got := h.conv.sendCallCount(); got != 0 {
		t.Fatalf("provider calls after failed write = %d, want 0", got)
	}
	before, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil || len(before.Turns) != 0 || len(before.Messages) != 0 {
		t.Fatalf("snapshot after failed write: turns=%d messages=%d err=%v", len(before.Turns), len(before.Messages), err)
	}

	if _, err := h.svc.Send(ctx, testSession, msg); err != nil {
		t.Fatalf("retry after recovery: %v", err)
	}
	if _, err := h.svc.Send(ctx, testSession, msg); err != nil {
		t.Fatalf("duplicate retry: %v", err)
	}
	if got := h.conv.sendCallCount(); got != 1 {
		t.Fatalf("provider calls after retries = %d, want 1", got)
	}
	after, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil || len(after.Turns) != 1 || len(after.Messages) != 1 {
		t.Fatalf("snapshot after retries: turns=%d messages=%d err=%v", len(after.Turns), len(after.Messages), err)
	}
}

func TestSendRetryAfterProviderAcceptedButBindENOSPC(t *testing.T) {
	h := newHarnessWithConversationAndStore(t, nil, func(st *store.Store) chatsvc.Store {
		return &enospcBindStore{Store: st}
	})
	ctx := context.Background()
	msg := ports.ChatUserMessage{Text: "inspect this", ClientMessageID: "enospc-bind", Origin: domain.MessageOriginHuman}

	if _, err := h.svc.Send(ctx, testSession, msg); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("first Send error = %v, want ENOSPC", err)
	}
	if _, err := h.svc.Send(ctx, testSession, msg); err != nil {
		t.Fatalf("retry after bind failure: %v", err)
	}
	if got := h.conv.sendCallCount(); got != 1 {
		t.Fatalf("provider calls after bind failure and retry = %d, want 1", got)
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil || len(snapshot.Turns) != 1 || len(snapshot.Messages) != 1 {
		t.Fatalf("snapshot after bind failure: turns=%d messages=%d err=%v", len(snapshot.Turns), len(snapshot.Messages), err)
	}
}
