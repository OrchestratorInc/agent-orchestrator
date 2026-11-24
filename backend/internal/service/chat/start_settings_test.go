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

func TestStartSavesANewConversationsFastModeAndAutoCompaction(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	created, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Mode: domain.SessionModeChat, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created.ID
	newService := func(started *ports.ChatStartConfig, resumed *ports.ChatResumeConfig) *chatsvc.Service {
		conv := newFakeConversation()
		conv.providerConversationID = "claude-thread"
		return chatsvc.New(chatsvc.Options{
			Store: st, Reader: fullSnapshotReader(st), Sessions: st,
			Drivers: fakeRegistry{driver: fakeDriver{conv: conv, startCfg: started, resumeCfg: resumed}},
			Log:     slog.New(slog.DiscardHandler), NewID: uuid.NewString,
		})
	}

	var started ports.ChatStartConfig
	svc := newService(&started, nil)
	controller, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(), FastMode: true, AutoCompactWindow: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Fast mode is a setting of the running agent; the window is saved.
	if !controller.Settings().FastMode {
		t.Fatalf("live settings = %+v, want Fast mode on", controller.Settings())
	}
	if got := started.Env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "1000000" {
		t.Fatalf("compaction window = %q, want the picked 1m", got)
	}
	stored, err := st.ConversationForSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Settings.AutoCompactWindow != "1m" {
		t.Fatalf("saved settings = %+v, want a 1m window", stored.Settings)
	}
	if err := svc.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}

	// A resume carries no starting choice and keeps the saved one.
	var resumed ports.ChatResumeConfig
	svc = newService(nil, &resumed)
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(), ProviderConversationID: "claude-thread",
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, id) })
	if got := resumed.Env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "1000000" {
		t.Fatalf("resumed compaction window = %q, want the saved 1m", got)
	}
	stored, err = st.ConversationForSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Settings.AutoCompactWindow != "1m" {
		t.Fatalf("settings after resume = %+v, want the window kept", stored.Settings)
	}
}
