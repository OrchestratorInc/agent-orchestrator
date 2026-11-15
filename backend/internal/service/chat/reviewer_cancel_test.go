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
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// Exercise the launcher boundary with real owner-scoped controllers and SQLite.
type reviewerCancelBridge struct {
	reviewcore.ReviewerChatController
	svc *chatsvc.Service
}

func (b reviewerCancelBridge) InterruptReviewChat(ctx context.Context, id string) error {
	return b.svc.InterruptForOwner(ctx, domain.ReviewConversationOwner(id))
}

func (b reviewerCancelBridge) StopReviewChat(ctx context.Context, id string) error {
	return b.svc.StopForOwner(ctx, domain.ReviewConversationOwner(id))
}

type interruptingReviewConversation struct{ *fakeConversation }

func (c interruptingReviewConversation) Interrupt(_ context.Context, turn string) error {
	c.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn, TurnState: domain.TurnStateInterrupted})
	return nil
}

func TestSidebarReviewCancelKeepsChatUsableAndWorkerIsolated(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	now := time.Now().UTC()
	if err := st.UpsertReview(ctx, domain.Review{ID: "review-stop", SessionID: testSession, ProjectID: testProject,
		Harness: domain.ReviewerCodex, InterfaceMode: domain.ReviewerInterfaceChat, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	worker, reviewer := newFakeConversation(), newFakeConversation()
	for _, provider := range []*fakeConversation{worker, reviewer} {
		provider.onSend = func(turn string) {
			provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn})
		}
	}
	var closed atomic.Bool
	reviewer.onClose = func() { closed.Store(true) }
	var ids atomic.Uint64
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Reader: fullSnapshotReader(st), Log: slog.New(slog.DiscardHandler),
		NewID: func() string { return fmt.Sprintf("review-stop-%d", ids.Add(1)) },
		Drivers: fakeRegistry{driver: fakeDriver{start: func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
			if cfg.SessionID == testSession {
				return worker, nil
			}
			return interruptingReviewConversation{reviewer}, nil
		}}}})
	t.Cleanup(func() { svc.StopAll(context.Background()) })
	owner := domain.ReviewConversationOwner("review-stop")
	for _, o := range []domain.ConversationOwner{domain.SessionConversationOwner(testSession), owner} {
		if _, err := svc.Start(ctx, chatsvc.StartConfig{Owner: o, SessionID: testSession, ProjectID: testProject,
			Harness: domain.HarnessCodex, WorkspacePath: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SendForOwner(ctx, o, ports.ChatUserMessage{Text: "running"}); err != nil {
			t.Fatal(err)
		}
	}
	conversation, err := st.ConversationForReview(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	awaitStoreSnapshot(t, st, conversation.ID, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := svc.SendForOwner(ctx, owner, ports.ChatUserMessage{Text: "queued"}); err != nil {
		t.Fatal(err)
	}
	launcher := reviewcore.NewLauncher(nil, nil, t.TempDir(), reviewcore.WithReviewerChat(reviewerCancelBridge{svc: svc}))
	if err := launcher.Cancel(ctx, "review-chat:review-stop", domain.ReviewerCodex); err != nil {
		t.Fatal(err)
	}
	snapshot := awaitStoreSnapshot(t, st, conversation.ID, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 2 && s.Turns[0].State.Terminal() && s.Turns[1].State.Terminal()
	})
	for _, turn := range snapshot.Turns {
		if turn.State != domain.TurnStateInterrupted || turn.ErrorMessage != "" {
			t.Fatalf("cancelled turn = %+v; want interrupted without an error", turn)
		}
	}
	if closed.Load() || !svc.HasLiveControllerForOwner(owner) || len(reviewer.sentTexts()) != 1 {
		t.Fatal("Stop must retain the controller and cancel queued review work")
	}
	workerSnapshot, err := svc.Snapshot(ctx, testSession)
	if err != nil || len(workerSnapshot.Turns) != 1 || workerSnapshot.Turns[0].State != domain.TurnStateRunning {
		t.Fatalf("worker changed during reviewer cancellation: %+v, %v", workerSnapshot, err)
	}
	if _, err := svc.SendForOwner(ctx, owner, ports.ChatUserMessage{Text: "continue"}); err != nil {
		t.Fatalf("Chat unusable after Stop: %v", err)
	}
	if err := launcher.Destroy(ctx, "review-chat:review-stop"); err != nil {
		t.Fatal(err)
	}
	if !closed.Load() || svc.HasLiveControllerForOwner(owner) || !svc.HasLiveChatController(testSession) {
		t.Fatal("Archive teardown must close only the reviewer")
	}
}
