package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/OrchestratorInc/agent-orchestrator/backend/internal/service/chat"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// Older daemons stored a replayed limit notice as a running row on a turn that
// had long settled. A daemon restart that reattaches to the live chat skips the
// orphaned-work cleanup, so stranded rows are settled outside that gate.
func TestServiceLiveReconnectSettlesRowsStrandedOnSettledTurns(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	reader := fullSnapshotReader(st)
	var ids atomic.Int32
	newID := func() string { return fmt.Sprintf("stranded-%d", ids.Add(1)) }
	limit := "You've hit your 5-hour limit · resets 4:50pm (Asia/Calcutta)"

	firstProvider := &terminatingConversation{fakeConversation: newFakeConversation()}
	first := chatsvc.New(chatsvc.Options{
		Store: st, Reader: reader, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: firstProvider}},
		Log:     slog.New(slog.DiscardHandler), NewID: newID,
	})
	firstController, err := first.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	turn, err := firstController.Send(ctx, ports.ChatUserMessage{Text: "keep working"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	firstProvider.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID,
		ProviderConversationID: firstProvider.ProviderConversationID(),
	})
	firstProvider.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(limit),
	})
	h := &harness{st: st, ctrl: firstController}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, candidate := range s.Turns {
			if candidate.ID == turn.ID {
				return candidate.State == domain.TurnStateFailed
			}
		}
		return false
	})
	first.StopAll(ctx)

	// What an older daemon's history replay left behind on the failed turn.
	if err := st.UpsertActivity(ctx, firstController.ConversationID(), turn.ProviderTurnID, domain.ConversationActivity{
		ID: "stranded-limit", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusRunning,
		Summary: limit, ProviderItemID: "session-failure:replayed",
		Detail: []byte(`{"event":"provider.failure","category":"limit"}`),
	}, time.Now()); err != nil {
		t.Fatalf("seed stranded row: %v", err)
	}

	secondProvider := &liveReconnectedConversation{nativeHistoryConversation: &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
	}}
	second := chatsvc.New(chatsvc.Options{
		Store: st, Reader: reader, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: secondProvider}},
		Log:     slog.New(slog.DiscardHandler), NewID: newID,
	})
	t.Cleanup(func() { second.StopAll(context.Background()) })
	if _, err := second.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(), ProviderConversationID: firstProvider.ProviderConversationID(),
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if !result.LiveReconnect {
				t.Error("restart was not a live reconnect; the test would not cover the gated path")
			}
			return chatsvc.ControllerCommit{}, nil
		},
	}); err != nil {
		t.Fatalf("reconnect Start: %v", err)
	}

	status, found, err := st.ActivityStatusByProviderItem(ctx, firstController.ConversationID(), "session-failure:replayed")
	if err != nil || !found {
		t.Fatalf("read stranded row: found=%v err=%v", found, err)
	}
	if status != domain.ActivityStatusFailed {
		t.Fatalf("stranded row after live reconnect = %q, want failed", status)
	}
}
