package chat_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type failingInstructionsStore struct {
	*store.Store
	fail atomic.Bool
}

func (s *failingInstructionsStore) GetSessionAdditionalInstructions(ctx context.Context, id domain.SessionID) (string, error) {
	if s.fail.Load() {
		return "", errors.New("instructions read failed")
	}
	return s.Store.GetSessionAdditionalInstructions(ctx, id)
}

func TestAdditionalInstructionsSteerOnce(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	if err := h.svc.SetAdditionalInstructions(ctx, testSession, "steer policy"); err != nil {
		t.Fatal(err)
	}
	msg := ports.ChatUserMessage{Text: "guidance", ClientMessageID: "instructions-steer"}
	if _, err := h.svc.Steer(ctx, testSession, msg); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Steer(ctx, testSession, msg); err != nil {
		t.Fatal(err)
	}
	calls := provider.steers()
	if len(calls) != 1 || strings.Count(calls[0].msg.Text, "<ao-user-additional-instructions>") != 1 {
		t.Fatalf("steer deliveries = %+v", calls)
	}
}

func TestAdditionalInstructionsReadFailureSettlesSend(t *testing.T) {
	var wrapped *failingInstructionsStore
	h := newHarnessWithConversationAndStore(t, nil, func(st *store.Store) chatsvc.Store {
		wrapped = &failingInstructionsStore{Store: st}
		return wrapped
	})
	wrapped.fail.Store(true)
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "never sent", ClientMessageID: "instructions-failure"}); err == nil {
		t.Fatal("read failure accepted")
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateFailed
	})
	if sent := h.conv.sentTexts(); len(sent) != 0 {
		t.Fatalf("provider received %v", sent)
	}
}

func TestAdditionalInstructionsReadFailureSettlesQueuedDelivery(t *testing.T) {
	var wrapped *failingInstructionsStore
	h := newHarnessWithConversationAndStore(t, nil, func(st *store.Store) chatsvc.Store { wrapped = &failingInstructionsStore{Store: st}; return wrapped })
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "failure-queue-1"}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "second", ClientMessageID: "failure-queue-2"})
	if err != nil || queued.State != domain.TurnStateQueued {
		t.Fatalf("queue = %+v, %v", queued, err)
	}
	wrapped.fail.Store(true)
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["second"] == domain.TurnStateFailed
	})
	if sent := h.conv.sentTexts(); len(sent) != 1 {
		t.Fatalf("provider received %v", sent)
	}
}

func TestAdditionalInstructionsReadFailureRejectsSteer(t *testing.T) {
	var wrapped *failingInstructionsStore
	h, provider := steerHarnessWithStore(t, func(st *store.Store) chatsvc.Store { wrapped = &failingInstructionsStore{Store: st}; return wrapped })
	ctx := context.Background()
	wrapped.fail.Store(true)
	msg := ports.ChatUserMessage{Text: "guidance", ClientMessageID: "failure-steer"}
	if _, err := h.svc.Steer(ctx, testSession, msg); !errors.Is(err, chatsvc.ErrSteerInstructionsUnavailable) || errors.Is(err, chatsvc.ErrSteerContentUnsupported) {
		t.Fatalf("read failure classification = %v", err)
	}
	wrapped.fail.Store(false)
	if _, err := h.svc.Steer(ctx, testSession, msg); !errors.Is(err, chatsvc.ErrSteerInstructionsUnavailable) || errors.Is(err, chatsvc.ErrSteerContentUnsupported) {
		t.Fatalf("replayed failure classification = %v", err)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("provider received %+v", calls)
	}
}

func TestAdditionalInstructionsNormalAndQueuedDelivery(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.SetAdditionalInstructions(ctx, testSession, "first policy"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "instructions-1"}); err != nil {
		t.Fatal(err)
	}
	sent := h.conv.sentTexts()
	if len(sent) != 1 || strings.Count(sent[0], "<ao-user-additional-instructions>") != 1 || !strings.Contains(sent[0], "first policy") {
		t.Fatalf("normal delivery = %v", sent)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "second", ClientMessageID: "instructions-2"})
	if err != nil || queued.State != domain.TurnStateQueued {
		t.Fatalf("queue = %+v, %v", queued, err)
	}
	if err := h.svc.SetAdditionalInstructions(ctx, testSession, "updated policy"); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["second"] == domain.TurnStateRunning
	})
	sent = h.conv.sentTexts()
	if len(sent) != 2 || strings.Count(sent[1], "<ao-user-additional-instructions>") != 1 || !strings.Contains(sent[1], "updated policy") || strings.Contains(sent[1], "first policy") {
		t.Fatalf("queue delivery = %v", sent)
	}
}
