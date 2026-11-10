package sessionmanager

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func newSQLiteTerminalManager(t *testing.T, st *sqlite.Store, dataDir string, rt *fakeRuntime, msg *fakeMessenger) *Manager {
	t.Helper()
	return New(Deps{
		Runtime: rt, Agents: singleAgent{agent: afterStartAgent{recordingAgent: &recordingAgent{}}},
		Workspace: &fakeWorkspace{path: t.TempDir()}, Store: st, Messenger: msg,
		Lifecycle: lifecycle.New(st, nil), DataDir: dataDir,
		LookPath: func(string) (string, error) { return "/bin/true", nil },
		Logger:   slog.New(slog.DiscardHandler),
	})
}

// The desktop's path through real storage: a client request id, an answer
// before any work, a daemon that dies before the background start runs, a
// restart that settles the start as failed, and a retry that delivers the
// opening prompt to exactly one agent.
func TestAsyncTerminalStartSurvivesRestartThroughSQLite(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dataDir)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID: "mer", Path: dataDir, Config: testRoleAgents(), RegisteredAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	first := newSQLiteTerminalManager(t, st, dataDir, &fakeRuntime{}, &fakeMessenger{})
	first.runBackground = func(func()) {} // the daemon dies before the start runs
	cfg := asyncTerminalConfig("fix the button")
	cfg.ClientRequestID, cfg.ClientRequestHash = "draft-1", "v1:fix the button"
	rec, _, _, err := first.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok, err := st.GetSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("GetSession: ok=%t err=%v", ok, err)
	}
	if stored.ProvisionState != domain.SessionProvisionProvisioning || stored.Mode != domain.SessionModeTUI ||
		stored.Metadata.Prompt != "fix the button" || !stored.ClientRequestCommitted {
		t.Fatalf("accepted row = state %q mode %q prompt %q committed %t",
			stored.ProvisionState, stored.Mode, stored.Metadata.Prompt, stored.ClientRequestCommitted)
	}

	rt := &fakeRuntime{}
	msg := &fakeMessenger{}
	second := newSQLiteTerminalManager(t, st, dataDir, rt, msg)
	var deferred []func()
	second.runBackground = func(work func()) { deferred = append(deferred, work) }
	if err := second.FailInterruptedProvisioning(ctx); err != nil {
		t.Fatal(err)
	}
	stored, _, _ = st.GetSession(ctx, rec.ID)
	if stored.ProvisionState != domain.SessionProvisionFailed || stored.IsTerminated || rt.created != 0 || len(msg.msgs) != 0 {
		t.Fatalf("after restart: state %q terminated %t runtimes %d prompts %v", stored.ProvisionState, stored.IsTerminated, rt.created, msg.msgs)
	}

	if _, err := second.ResumeAgentWithMode(ctx, rec.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(deferred) != 1 {
		t.Fatalf("background starts = %d, want 1", len(deferred))
	}
	deferred[0]()
	stored, _, _ = st.GetSession(ctx, rec.ID)
	if stored.ProvisionState != domain.SessionProvisionReady || stored.Metadata.RuntimeHandleID == "" || stored.Metadata.WorkspacePath == "" {
		t.Fatalf("after retry: state %q handle %q path %q", stored.ProvisionState, stored.Metadata.RuntimeHandleID, stored.Metadata.WorkspacePath)
	}
	if rt.created != 1 || !slices.Equal(msg.msgs, []string{"fix the button"}) {
		t.Fatalf("runtimes %d prompts %v, want one agent with the prompt once", rt.created, msg.msgs)
	}
}
