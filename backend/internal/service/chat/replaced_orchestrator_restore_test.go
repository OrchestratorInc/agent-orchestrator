package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// Restoring an orchestrator after a newer one took the project conversation
// must say so, on both restore paths that keep the previous owner.
func TestStartReplacedOrchestratorReportsReplacement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		scopeID string
	}{
		{name: "native resume"},
		{name: "coordinator reserved boundary", scopeID: "old-boundary:provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC()
			st, oldID := openProvisioningStore(t, domain.SessionProvisionReady)
			old, ok, err := st.GetSession(ctx, oldID)
			if err != nil || !ok {
				t.Fatalf("read old session: found=%t err=%v", ok, err)
			}
			old.Kind = domain.KindOrchestrator
			old.Metadata.ProviderConversationID = "old-thread"
			if err := st.UpdateSession(ctx, old); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreateConversation(ctx, "project-chat", domain.ConversationScopeProject, testProject, oldID, now); err != nil {
				t.Fatal(err)
			}
			newer, err := st.CreateSession(ctx, domain.SessionRecord{
				ProjectID: testProject, Kind: domain.KindOrchestrator,
				Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
				ProvisionState: domain.SessionProvisionReady, CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.CreateConversation(ctx, "unused", domain.ConversationScopeProject, testProject, newer.ID, now); err != nil {
				t.Fatalf("rebind to newer orchestrator: %v", err)
			}

			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st, Reader: fullSnapshotReader(st),
				Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
				Log:     slog.New(slog.DiscardHandler), NewID: sequentialIDs("replaced"),
			})
			t.Cleanup(func() { _ = svc.Stop(context.Background(), oldID) })
			_, err = svc.Start(ctx, chatsvc.StartConfig{
				SessionID: oldID, ProjectID: testProject, Kind: domain.KindOrchestrator,
				Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
				ProviderConversationID: "old-thread", ProviderScopeID: tc.scopeID,
			})
			if !errors.Is(err, ports.ErrProjectConversationReplaced) {
				t.Fatalf("Start error = %v, want ErrProjectConversationReplaced", err)
			}
			owner, err := st.ConversationForSession(ctx, newer.ID)
			if err != nil || owner.ID != "project-chat" {
				t.Fatalf("newer orchestrator lost the conversation: %+v err=%v", owner, err)
			}
		})
	}
}
