package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

const rollbackCrash = "daemon stopped during the rollback"

// rollbackCrashStore lets the switch publish the target, then stops the daemon
// at the rollback's write, before it commits.
type rollbackCrashStore struct {
	*sqlite.Store
	publishes int
}

func (s *rollbackCrashStore) UpdateSessionWithLaunchOptions(ctx context.Context, rec domain.SessionRecord, options domain.AgentLaunchOptions) error {
	if s.publishes++; s.publishes > 1 {
		panic(rollbackCrash)
	}
	return s.Store.UpdateSessionWithLaunchOptions(ctx, rec, options)
}

func (s *rollbackCrashStore) SetSessionLaunchOptions(context.Context, domain.SessionID, domain.AgentLaunchOptions) error {
	panic("the rollback wrote the launch options apart from ownership")
}

func TestFailedTerminalSwitchRollbackMovesOwnerAndOptionsTogether(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	raw := sqlitetest.MustOpen(t)
	if err := raw.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: at, Config: testRoleAgents()}); err != nil {
		t.Fatal(err)
	}
	rec, err := raw.CreateSession(ctx, domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Mode: domain.SessionModeTUI, CreatedAt: at, UpdatedAt: at, Activity: domain.Activity{State: domain.ActivityExited, LastActivityAt: at},
		Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir(), Branch: "branch", RuntimeHandleID: "source-runtime",
			RuntimeLaunchID: "source-launch", AgentSessionID: "source-native", Prompt: "ORIGINAL TASK"}})
	if err != nil {
		t.Fatal(err)
	}
	store := &rollbackCrashStore{Store: raw}
	manager := New(Deps{Runtime: &fakeRuntime{createErr: errors.New("target failed to start")}, Agents: singleAgent{agent: &recordingAgent{}},
		Workspace: &fakeWorkspace{}, Store: store, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: newFakeStore()},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	func() {
		defer func() {
			if got := recover(); got != rollbackCrash {
				t.Fatalf("stopped with %v, want the rollback's single write", got)
			}
		}()
		_ = manager.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessCodex, HandoffNote: "TARGET HANDOFF", FastMode: true})
	}()
	got, _, err := raw.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	options, err := raw.SessionLaunchOptions(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing of the rollback landed, so the target still owns the session with
	// its own note: a Resume hands over again instead of replaying the task.
	if got.Harness != domain.HarnessCodex || options.HandoffNote != "TARGET HANDOFF" {
		t.Fatalf("after the stop: owner %q with options %+v, want the target with its handoff", got.Harness, options)
	}
}
