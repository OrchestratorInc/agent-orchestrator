package chat_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestReaddedProjectOrchestratorOwnsFreshProviderBoundary(t *testing.T) {
	for _, priorThread := range []string{"", "old-codex-thread"} {
		for _, queued := range []bool{false, true} {
			t.Run(fmt.Sprintf("prior_thread=%t/queued=%t", priorThread != "", queued), func(t *testing.T) {
				ctx := context.Background()
				now := time.Now().UTC()
				st, oldID := openProvisioningStore(t, domain.SessionProvisionReady)
				old, ok, err := st.GetSession(ctx, oldID)
				if err != nil || !ok {
					t.Fatalf("read old session: found=%t err=%v", ok, err)
				}
				old.Kind = domain.KindOrchestrator
				old.Metadata.ProviderConversationID = priorThread
				if err := st.UpdateSession(ctx, old); err != nil {
					t.Fatal(err)
				}
				project, err := st.CreateConversation(ctx, "old-project-chat", domain.ConversationScopeProject, testProject, oldID, now)
				if err != nil {
					t.Fatal(err)
				}
				if err := st.UpsertActivity(ctx, project.ID, "", domain.ConversationActivity{
					ID: "old-activity", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
					Summary: "Previous orchestrator started",
				}, now); err != nil {
					t.Fatal(err)
				}
				old.IsTerminated = true
				if err := st.UpdateSession(ctx, old); err != nil {
					t.Fatal(err)
				}
				if archived, err := st.ArchiveProject(ctx, string(testProject), now); err != nil || !archived {
					t.Fatalf("remove project: archived=%t err=%v", archived, err)
				}
				if err := st.UpsertProject(ctx, domain.ProjectRecord{
					ID: string(testProject), Path: t.TempDir(), RegisteredAt: now,
				}); err != nil {
					t.Fatal(err)
				}
				fresh, err := st.CreateSession(ctx, domain.SessionRecord{
					ProjectID: testProject, Kind: domain.KindOrchestrator,
					Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
					ProvisionState: domain.SessionProvisionProvisioning, CreatedAt: now, UpdatedAt: now,
				})
				if err != nil {
					t.Fatal(err)
				}
				svc := chatsvc.New(chatsvc.Options{
					Store: st, Sessions: st, Reader: fullSnapshotReader(st),
					Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
					Log:     slog.New(slog.DiscardHandler), NewID: sequentialIDs("readded"),
				})
				t.Cleanup(func() { _ = svc.Stop(context.Background(), fresh.ID) })
				if queued {
					if _, err := svc.Send(ctx, fresh.ID, ports.ChatUserMessage{Text: "new project prompt", Origin: domain.MessageOriginHuman}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := svc.Start(ctx, chatsvc.StartConfig{
					SessionID: fresh.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
					Harness: domain.HarnessClaudeCode, WorkspacePath: t.TempDir(),
					ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
						if result.ProviderBoundary == nil {
							return chatsvc.ControllerCommit{}, fmt.Errorf("new orchestrator did not reserve its own provider branch")
						}
						fresh.ProvisionState = domain.SessionProvisionReady
						fresh.Metadata.ProviderConversationID = result.ProviderConversationID
						fresh.Metadata.ControllerGeneration = result.ControllerGeneration
						if err := st.CommitChatSpawn(ctx, fresh, *result.ProviderBoundary); err != nil {
							return chatsvc.ControllerCommit{}, err
						}
						committed := result.Conversation
						committed.ActiveBranchID = result.ProviderBoundary.ID
						return chatsvc.ControllerCommit{Conversation: committed}, nil
					},
				}); err != nil {
					t.Fatal(err)
				}
				current, err := st.ConversationForSession(ctx, fresh.ID)
				if err != nil {
					t.Fatal(err)
				}
				branch, err := st.ConversationBranch(ctx, current.ID, current.ActiveBranchID)
				if err != nil || branch.SessionID != fresh.ID || branch.ProviderConversationID != fresh.Metadata.ProviderConversationID || branch.ParentBranchID != project.ActiveBranchID {
					t.Fatalf("new session branch: %+v err=%v", branch, err)
				}
				parent, err := st.ConversationBranch(ctx, project.ID, project.ActiveBranchID)
				if err != nil || parent.SessionID != oldID || parent.ProviderConversationID != priorThread {
					t.Fatalf("old session branch changed: %+v err=%v", parent, err)
				}
				if err := svc.Stop(ctx, fresh.ID); err != nil {
					t.Fatal(err)
				}
				svc = chatsvc.New(chatsvc.Options{
					Store: st, Sessions: st, Reader: fullSnapshotReader(st),
					Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation()}},
					Log:     slog.New(slog.DiscardHandler), NewID: sequentialIDs("cold"),
				})
				if _, err := svc.Start(ctx, chatsvc.StartConfig{
					SessionID: fresh.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
					Harness: domain.HarnessClaudeCode, WorkspacePath: t.TempDir(),
					ProviderConversationID: fresh.Metadata.ProviderConversationID,
				}); err != nil {
					t.Fatalf("cold resume after re-adding project: %v", err)
				}
			})
		}
	}
}
