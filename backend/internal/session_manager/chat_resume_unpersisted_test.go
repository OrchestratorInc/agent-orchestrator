package sessionmanager

import (
	"context"
	"testing"
	"time"

	claudeagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestResumeUntouchedChatStartsFreshWhenProviderNeverPersisted(t *testing.T) {
	const reserved = "92fadfcb-3d31-4dd6-93c1-097bf823cc19"
	for _, tc := range []struct {
		name         string
		kind         domain.SessionKind
		scope        domain.ConversationScope
		providerTurn bool
		wantFresh    bool
		replacement  bool
	}{
		{"orchestrator", domain.KindOrchestrator, domain.ConversationScopeProject, false, true, false},
		{"worker", domain.KindWorker, domain.ConversationScopeSession, false, true, false},
		{"orchestrator with provider turn", domain.KindOrchestrator, domain.ConversationScopeProject, true, false, false},
		{"replacement orchestrator", domain.KindOrchestrator, domain.ConversationScopeProject, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			ctx := context.Background()
			store := sqlitetest.MustOpenAt(t, t.TempDir())
			if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "proj", Path: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			created, err := store.CreateSession(ctx, domain.SessionRecord{
				ProjectID: "proj", Kind: tc.kind, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
				Metadata: domain.SessionMetadata{
					WorkspacePath: t.TempDir(), Branch: "ao/session",
					ProviderConversationID: reserved, ControllerGeneration: "chat-generation",
				},
				Activity:      domain.Activity{State: domain.ActivityExited, LastActivityAt: now},
				FirstSignalAt: now, CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			conversation, err := store.CreateConversation(ctx, "conversation-1", tc.scope, "proj", created.ID, now)
			if err != nil {
				t.Fatal(err)
			}
			if tc.providerTurn {
				if err := store.AdoptProviderTurn(ctx, conversation.ID, created.ID, "chat-generation",
					"turn-1", "provider-turn-1", now); err != nil {
					t.Fatal(err)
				}
			}
			if tc.replacement {
				replacement := created
				replacement.ID = ""
				replacement.Metadata.ProviderConversationID = ""
				replacement, err = store.CreateSession(ctx, replacement)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateConversation(ctx, "ignored", tc.scope, "proj", replacement.ID, now); err != nil {
					t.Fatal(err)
				}
				replacement.Metadata.ProviderConversationID = reserved
				if err := store.UpdateSession(ctx, replacement); err != nil {
					t.Fatal(err)
				}
				created = replacement
			}
			launcher := &recordingLauncher{}
			manager := New(Deps{
				Runtime: &fakeRuntime{}, Agents: singleAgent{agent: claudeagent.New()}, Workspace: &fakeWorkspace{},
				Store: store, Messenger: &fakeMessenger{}, Chat: launcher,
				Lifecycle: &sqliteTransitionLifecycle{store: store},
				LookPath:  func(string) (string, error) { return "/bin/true", nil },
			})
			manager.browserCapabilities = &recordingBrowserAuthority{authority: browsersvc.NewAuthority()}

			_, err = manager.ResumeAgentWithMode(ctx, created.ID)

			if len(launcher.started) != 1 {
				t.Fatalf("started %d chat controllers, want 1 (err=%v)", len(launcher.started), err)
			}
			start := launcher.started[0]
			if start.ProviderConversationID != reserved || start.ExpectedControllerOwner.ProviderConversationID != reserved {
				t.Fatalf("stored id was not resumed: start=%q owner=%q",
					start.ProviderConversationID, start.ExpectedControllerOwner.ProviderConversationID)
			}
			if start.FreshIfProviderConversationMissing != tc.wantFresh {
				t.Fatalf("fresh-if-missing = %v, want %v", start.FreshIfProviderConversationMissing, tc.wantFresh)
			}
			if !tc.wantFresh {
				return
			}
			if err != nil {
				t.Fatalf("ResumeAgentWithMode: %v", err)
			}
			assertFreshProviderOnRoot(t, store, created.ID, conversation)
		})
	}
}

func assertFreshProviderOnRoot(t *testing.T, store *sqlite.Store, id domain.SessionID, conversation domain.ConversationRecord) {
	t.Helper()
	ctx := context.Background()
	current, _, err := store.GetSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := store.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Metadata.ProviderConversationID != "thread-1" || branch.ProviderConversationID != "thread-1" ||
		branch.ParentBranchID != "" {
		t.Fatalf("fresh provider was not bound to the root: session=%q branch=%+v",
			current.Metadata.ProviderConversationID, branch)
	}
}
