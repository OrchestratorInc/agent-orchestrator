package chat_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// 真实 SQLite 和 Chat 启动路径证明独立会话不会接管项目历史或彼此的上下文。
func TestIndependentOrchestratorsKeepSeparateConversations(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	primary, err := st.CreateConversation(ctx, "primary", domain.ConversationScopeProject, testProject, testSession, now)
	if err != nil {
		t.Fatal(err)
	}
	driver := fakeDriver{start: func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		conv := newFakeConversation()
		conv.providerConversationID = "thread-" + cfg.ProviderScopeID
		return conv, nil
	}}
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Drivers: fakeRegistry{driver: driver}, Log: slog.New(slog.DiscardHandler), NewID: uuid.NewString})
	seen := map[string]bool{primary.ID: true}
	for range 2 {
		rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: testProject, Kind: domain.KindOrchestrator,
			IndependentOrchestrator: true, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		id := rec.ID
		if _, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: id, ProjectID: testProject, Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = svc.Stop(context.Background(), id) })
		conv, err := st.ConversationForSession(ctx, id)
		if err != nil || conv.Scope != domain.ConversationScopeSession || conv.SessionID != id || seen[conv.ID] {
			t.Fatalf("independent conversation = %+v, err = %v", conv, err)
		}
		seen[conv.ID] = true
	}
	current, err := st.ProjectConversation(ctx, testProject)
	if err != nil || current.ID != primary.ID || current.SessionID != testSession {
		t.Fatalf("primary changed: %+v, %v", current, err)
	}
}
