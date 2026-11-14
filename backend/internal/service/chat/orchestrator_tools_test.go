package chat_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// Drivers learn that a session is a project orchestrator, so a provider can
// pre-approve the commands it coordinates workers with (#8398).
func TestStartTellsTheDriverWhetherTheSessionIsAnOrchestrator(t *testing.T) {
	for _, tt := range []struct {
		kind domain.SessionKind
		want bool
	}{
		{domain.KindOrchestrator, true},
		{domain.KindWorker, false},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			st := openStore(t)
			var started ports.ChatStartConfig
			ids := 0
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st,
				Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation(), startCfg: &started}},
				Log:     slog.New(slog.DiscardHandler),
				NewID:   func() string { ids++; return fmt.Sprintf("id-%03d", ids) },
			})
			if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Kind: tt.kind,
				Harness: domain.HarnessClaudeCode, WorkspacePath: t.TempDir(),
			}); err != nil {
				t.Fatalf("Start: %v", err)
			}
			t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
			if started.Orchestrator != tt.want {
				t.Fatalf("Orchestrator = %v, want %v", started.Orchestrator, tt.want)
			}
		})
	}
}
