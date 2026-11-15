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
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func neverStartedChatOrchestrator(
	t *testing.T,
	lifecycle func(*sqlite.Store) lifecycleRecorder,
) (*Manager, *sqlite.Store, *transitionChat, *[]string, domain.SessionID) {
	t.Helper()
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
		Metadata:  domain.SessionMetadata{WorkspacePath: t.TempDir(), Branch: "ao/session"},
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConversationForSession(ctx, created.ID); err == nil {
		t.Fatal("fixture unexpectedly has a conversation row")
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
		Lifecycle: lifecycle(store),
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	useFastInterfaceTransitionTimings(manager)
	manager.browserCapabilities = &recordingBrowserAuthority{authority: browsersvc.NewAuthority()}
	return manager, store, chat, log, created.ID
}

func settleSQLiteTransition(t *testing.T, store *sqlite.Store, id string) domain.SessionInterfaceTransition {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, ok, err := store.GetSessionInterfaceTransition(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if ok && current.Phase.Terminal() {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("interface transition did not settle: %+v", current)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestInterfaceTransitionNeverStartedChatSwitchesToTerminal(t *testing.T) {
	manager, store, _, log, id := neverStartedChatOrchestrator(t, func(store *sqlite.Store) lifecycleRecorder {
		return &sqliteTransitionLifecycle{store: store}
	})
	ctx := context.Background()
	transition, err := manager.StartInterfaceTransition(ctx, id, domain.SessionModeTUI,
		domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatalf("StartInterfaceTransition: %v", err)
	}
	settled := settleSQLiteTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("switch = %s (%s): %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
	}
	current, _, err := store.GetSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if domain.NormalizeSessionMode(current.Mode) != domain.SessionModeTUI || current.Metadata.ProviderConversationID != "" {
		t.Fatalf("session after switch = mode %s provider %q", current.Mode, current.Metadata.ProviderConversationID)
	}
	if got := (*log)[len(*log)-1]; got != "start:tui" {
		t.Fatalf("controller order = %v", *log)
	}
}

func TestInterfaceTransitionRollbackStartsNeverStartedChatFresh(t *testing.T) {
	manager, store, chat, _, id := neverStartedChatOrchestrator(t, func(store *sqlite.Store) lifecycleRecorder {
		return failingEpochLifecycle{&sqliteTransitionLifecycle{store: store}}
	})
	ctx := context.Background()
	transition, err := manager.StartInterfaceTransition(ctx, id, domain.SessionModeTUI,
		domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatalf("StartInterfaceTransition: %v", err)
	}
	settled := settleSQLiteTransition(t, store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionFailed || settled.ErrorCode != "SESSION_CHANGED" {
		t.Fatalf("rollback = %s (%s): %s", settled.Phase, settled.ErrorCode, settled.ErrorDetail)
	}
	current, _, err := store.GetSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if domain.NormalizeSessionMode(current.Mode) != domain.SessionModeChat {
		t.Fatalf("source mode was not restored: %s", current.Mode)
	}
	if chat.start.ExpectedControllerOwner.ProviderConversationID != "" {
		t.Fatalf("rollback resumed a provider id the session never held: %+v", chat.start.ExpectedControllerOwner)
	}
}

func TestInterfaceTransitionRefusesSessionWithoutWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name      string
		provision domain.SessionProvisionState
		refused   bool
	}{
		{"start failed", domain.SessionProvisionFailed, true},
		{"no provisioning record", "", true},
		{"still provisioning", domain.SessionProvisionProvisioning, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, store, _, log, id := neverStartedChatOrchestrator(t, func(store *sqlite.Store) lifecycleRecorder {
				return &sqliteTransitionLifecycle{store: store}
			})
			ctx := context.Background()
			rec, _, err := store.GetSession(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			rec.Metadata.WorkspacePath = ""
			if err := store.UpdateSession(ctx, rec); err != nil {
				t.Fatal(err)
			}
			if tc.provision != "" {
				if _, err := store.SetSessionProvisionState(ctx, id, tc.provision, "", time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			status, err := manager.InterfaceTransitionStatus(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if got := status.ReasonCode == "WORKSPACE_UNAVAILABLE"; got != tc.refused || (tc.refused && status.Supported) {
				t.Fatalf("status = %+v, want refused=%v", status, tc.refused)
			}
			if !tc.refused {
				return
			}
			_, err = manager.StartInterfaceTransition(ctx, id, domain.SessionModeTUI,
				domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
			if !errors.Is(err, ErrInterfaceWorkspaceUnavailable) {
				t.Fatalf("StartInterfaceTransition = %v, want ErrInterfaceWorkspaceUnavailable", err)
			}
			if _, found, err := store.GetLatestSessionInterfaceTransition(ctx, id); err != nil || found || len(*log) != 0 {
				t.Fatalf("refusal touched the session: transition=%v err=%v log=%v", found, err, *log)
			}
		})
	}
}
