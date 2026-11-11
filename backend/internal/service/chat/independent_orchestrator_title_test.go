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

// 独立编排器通过真实 Chat 与 SQLite 接受自动任务名，随后人工改名必须赢过提供方事件。
func TestIndependentOrchestratorTitlePreservesManualRename(t *testing.T) {
	ctx := context.Background()
	recorder := newHistoryRecorder()
	h := &harness{st: openStore(t), conv: recorder.fakeConversation, clock: time.Now().UTC()}
	rec, err := h.st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator, IndependentOrchestrator: true,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, CreatedAt: h.now(), UpdatedAt: h.now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	id := rec.ID
	var ids atomic.Int64
	h.svc = chatsvc.New(chatsvc.Options{
		Store: h.st, Sessions: h.st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: recorder}},
		Log:     slog.New(slog.DiscardHandler), Now: h.now,
		NewID: func() string { return fmt.Sprintf("title-%d", ids.Add(1)) },
	})
	h.ctrl, err = h.svc.Start(ctx, chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.svc.Stop(context.Background(), id) })
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventThreadRenamed, Title: "修复登录重定向"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.AppliedTitle == "修复登录重定向"
	})

	if renamed, err := h.st.RenameSession(ctx, id, "人工指定任务名", h.now()); err != nil || !renamed {
		t.Fatalf("人工改名: renamed=%v err=%v", renamed, err)
	}
	if _, err := h.svc.SetTitle(ctx, id, "新的自动任务名"); err != nil {
		t.Fatal(err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.ProviderTitle == "新的自动任务名"
	})
	rec, found, err := h.st.GetSession(ctx, id)
	if err != nil || !found || rec.DisplayName != "人工指定任务名" {
		t.Fatalf("自动命名覆盖人工名称: name=%q found=%v err=%v", rec.DisplayName, found, err)
	}
}
