package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	claudeagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type failingEpochLifecycle struct {
	*sqliteTransitionLifecycle
}

func (failingEpochLifecycle) CommitControllerEpoch(
	context.Context, domain.SessionID, domain.SessionMode, domain.SessionMode, string, bool,
) (bool, error) {
	return false, errors.New("commit controller epoch failed")
}

// When the controller epoch never commits, the Chat source still owns its
// provider id durably. Rollback must restore that owner, not present a
// memory-only fresh owner that the browser capability check then rejects.
func TestInterfaceTransitionRollbackRestoresUncommittedChatSource(t *testing.T) {
	// The fake runtime never executes Claude, but preflight uses its real adapter.
	binDir := t.TempDir()
	binary := "claude"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if err := os.WriteFile(filepath.Join(binDir, binary), []byte("test executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	store := sqlitetest.MustOpenAt(t, t.TempDir())
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "proj", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	created, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "proj", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{
			WorkspacePath: t.TempDir(), Branch: "ao/session",
			ProviderConversationID: "92fadfcb-3d31-4dd6-93c1-097bf823cc19", ControllerGeneration: "chat-generation",
		},
		Activity:      domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		FirstSignalAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, "conversation-1", domain.ConversationScopeProject, "proj", created.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	log := &[]string{}
	chat := &transitionChat{
		log: log, supportsChat: true,
		armed:   make(chan domain.SessionInterfaceTransitionPolicy, 1),
		aborted: make(chan struct{}, 1),
	}
	manager := New(Deps{
		Runtime: &transitionRuntime{fakeRuntime: &fakeRuntime{}, log: log}, Agents: singleAgent{agent: claudeagent.New()},
		Workspace: &fakeWorkspace{}, Store: store, Messenger: &fakeMessenger{}, Chat: chat,
		Lifecycle: failingEpochLifecycle{&sqliteTransitionLifecycle{store: store}},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	useFastInterfaceTransitionTimings(manager)
	manager.browserCapabilities = &recordingBrowserAuthority{authority: browsersvc.NewAuthority()}

	transition, err := manager.StartInterfaceTransition(ctx, created.ID, domain.SessionModeTUI,
		domain.SessionInterfaceTransitionInterrupt, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatalf("StartInterfaceTransition: %v", err)
	}
	var settled domain.SessionInterfaceTransition
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, ok, readErr := store.GetSessionInterfaceTransition(ctx, transition.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if ok && current.Phase.Terminal() {
			settled = current
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("interface transition did not settle: %+v", current)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if settled.Phase != domain.SessionInterfaceTransitionFailed || settled.ErrorCode != "SESSION_CHANGED" {
		t.Fatalf("rollback = %s (%s): %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
	}
	current, _, err := store.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if domain.NormalizeSessionMode(current.Mode) != domain.SessionModeChat {
		t.Fatalf("source mode was not restored: %s", current.Mode)
	}
	if chat.start.ExpectedControllerOwner.ProviderConversationID != "92fadfcb-3d31-4dd6-93c1-097bf823cc19" ||
		!chat.start.FreshIfProviderConversationMissing {
		// Rollback resumes the durable owner and lets the driver start fresh only
		// if the provider cannot find the unpersisted id.
		t.Fatalf("restored Chat owner = %+v fresh-if-missing=%v",
			chat.start.ExpectedControllerOwner, chat.start.FreshIfProviderConversationMissing)
	}
	branch, err := store.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		t.Fatal(err)
	}
	if branch.ParentBranchID != "" {
		t.Fatalf("rollback created a provider boundary branch: %+v", branch)
	}
}
