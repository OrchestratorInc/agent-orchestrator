package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// The daemon's background pipeline prunes the raw message deltas of a
// long-settled turn on its first pass and stops with the context, before the
// store would close.
func TestStartCDCPrunesSettledArchiveDeltas(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	old := time.Now().UTC().Add(-30 * 24 * time.Hour).Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "retention", Path: "/tmp/retention", RegisteredAt: old}); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "retention", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: old},
		CreatedAt: old, UpdatedAt: old,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, "retention-conv", domain.ConversationScopeSession, "retention", session.ID, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimChatControllerGeneration(ctx, session.ID, "gen-1"); err != nil {
		t.Fatal(err)
	}
	if created, err := store.AppendUserMessage(ctx, conversation.ID, session.ID, "gen-1", domain.ConversationMessage{
		ID: "prompt", Text: "hi", Origin: domain.MessageOriginHuman,
	}, "turn", old); err != nil || !created {
		t.Fatalf("open turn: created=%v err=%v", created, err)
	}
	if err := store.BindTurnToProvider(ctx, "turn", "provider-turn", old); err != nil {
		t.Fatal(err)
	}
	for _, event := range []struct{ method, text string }{{"message.delta", "hello"}, {"turn.completed", ""}} {
		if _, err := store.ProjectProviderEvent(ctx, conversation.ID, session.ID, "gen-1", "", event.method, `{}`, old.Add(time.Second),
			func(txCtx context.Context) error {
				if event.method == "message.delta" {
					return store.AppendAssistantDelta(txCtx, conversation.ID, "answer", "provider-turn", event.text, "answer-msg", old)
				}
				return store.SettleTurn(txCtx, conversation.ID, "provider-turn", domain.TurnStateCompleted, "", old.Add(time.Second))
			}); err != nil {
			t.Fatal(err)
		}
	}

	saved := archiveRetentionConfig
	archiveRetentionConfig = chatsvc.ArchiveRetentionConfig{FirstDelay: time.Millisecond}
	t.Cleanup(func() { archiveRetentionConfig = saved })
	runCtx, cancel := context.WithCancel(ctx)
	pipe, err := startCDC(runCtx, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		events, err := store.ProviderEventsSince(ctx, conversation.ID, 0, 10)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if len(events) == 1 && events[0].Method == "turn.completed" {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("archive still holds %d rows, want only turn.completed", len(events))
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- pipe.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pipeline did not stop after cancellation")
	}
	snapshot, err := store.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil || len(snapshot.Messages) != 2 || snapshot.Messages[1].Text != "hello" {
		t.Fatalf("projection after prune = %+v, %v; want the prompt and the reply intact", snapshot.Messages, err)
	}
}
