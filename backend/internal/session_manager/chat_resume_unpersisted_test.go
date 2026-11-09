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

// Claude reserves a session id when a Chat opens but writes no transcript until
// the first prompt. Resuming that untouched Chat lets the driver start fresh if
// the provider cannot find the id, and moves the session and root to the new id.
func TestResumeUntouchedChatStartsFreshWhenProviderNeverPersisted(t *testing.T) {
	const reserved = "92fadfcb-3d31-4dd6-93c1-097bf823cc19"
	for _, tc := range []struct {
		name             string
		kind             domain.SessionKind
		scope            domain.ConversationScope
		providerTurn     bool
		wantFresh        bool
		replacement      bool
		inheritedHistory bool
		newBranchTurn    bool
	}{
		{name: "orchestrator", kind: domain.KindOrchestrator, scope: domain.ConversationScopeProject, wantFresh: true},
		{name: "worker", kind: domain.KindWorker, scope: domain.ConversationScopeSession, wantFresh: true},
		{name: "orchestrator with provider turn", kind: domain.KindOrchestrator, scope: domain.ConversationScopeProject, providerTurn: true},
		// The project conversation was handed to a replacement orchestrator,
		// whose root still records the orchestrator that created it.
		{name: "replacement orchestrator", kind: domain.KindOrchestrator, scope: domain.ConversationScopeProject, wantFresh: true, replacement: true},
		{name: "replacement with inherited history", kind: domain.KindOrchestrator, scope: domain.ConversationScopeProject, wantFresh: true, replacement: true, inheritedHistory: true},
		{name: "replacement with a new turn", kind: domain.KindOrchestrator, scope: domain.ConversationScopeProject, replacement: true, inheritedHistory: true, newBranchTurn: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// An empty Claude config dir: no transcript exists for the reserved id.
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
			if tc.providerTurn || tc.inheritedHistory {
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
				// The replacement's first Chat start publishes its reserved id.
				replacement.Metadata.ProviderConversationID = reserved
				if err := store.UpdateSession(ctx, replacement); err != nil {
					t.Fatal(err)
				}
				created = replacement
				if tc.inheritedHistory {
					// The replacement starts on a new branch after the old
					// orchestrator has already contributed project history.
					child := domain.ConversationBranch{
						ID: "replacement-branch", ConversationID: conversation.ID, SessionID: replacement.ID,
						ProviderConversationID: reserved, ParentBranchID: conversation.ActiveBranchID,
						ForkAfterSequence: 1, CreatedAt: now,
					}
					if err := store.CreateAndActivateConversationBranch(ctx, replacement.ID, child, "chat-generation", now); err != nil {
						t.Fatal(err)
					}
					if tc.newBranchTurn {
						if err := store.AdoptProviderTurn(ctx, conversation.ID, replacement.ID, "chat-generation",
							"turn-2", "provider-turn-2", now); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			launcher := &recordingLauncher{}
			manager := New(Deps{
				Runtime: &fakeRuntime{}, Agents: singleAgent{agent: claudeagent.New()}, Workspace: &fakeWorkspace{},
				Store: store, Messenger: &fakeMessenger{}, Chat: launcher,
				Lifecycle: &sqliteTransitionLifecycle{store: store},
				LookPath:  func(string) (string, error) { return "/bin/true", nil },
			})
			// A live browser authority makes the launch prove the exact durable
			// controller owner, which is where a memory-only fresh start fails.
			manager.browserCapabilities = &recordingBrowserAuthority{authority: browsersvc.NewAuthority()}

			_, err = manager.ResumeAgentWithMode(ctx, created.ID)

			if len(launcher.started) != 1 {
				t.Fatalf("started %d chat controllers, want 1 (err=%v)", len(launcher.started), err)
			}
			start := launcher.started[0]
			// The stored id is always offered first: a surviving provider host may
			// still hold it, and only the driver can tell.
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
			// The launcher stands in for a driver that started fresh ("thread-1").
			if tc.inheritedHistory {
				current, _, err := store.GetSession(ctx, created.ID)
				if err != nil {
					t.Fatal(err)
				}
				active, err := store.ConversationForSession(ctx, created.ID)
				if err != nil {
					t.Fatal(err)
				}
				branch, err := store.ConversationBranch(ctx, active.ID, active.ActiveBranchID)
				if err != nil {
					t.Fatal(err)
				}
				if current.Metadata.ProviderConversationID != "thread-1" || branch.ProviderConversationID != "thread-1" || branch.ParentBranchID == "" {
					t.Fatalf("fresh provider was not bound to replacement branch: session=%q branch=%+v", current.Metadata.ProviderConversationID, branch)
				}
			} else {
				assertFreshProviderOnRoot(t, store, created.ID, conversation)
			}
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
