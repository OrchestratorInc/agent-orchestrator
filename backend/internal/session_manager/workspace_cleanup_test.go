package sessionmanager

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func newCleanupFixture(t *testing.T, command string) (*Manager, *fakeStore, *fakeWorkspace, domain.SessionRecord) {
	t.Helper()
	m, st, _, ws := newManager()
	m.dataDir = t.TempDir()
	rec := mkLive("mer-1")
	rec.Metadata.Branch = "ao/mer-1"
	rec.Metadata.WorkspacePath = filepath.Join(m.dataDir, "worktrees", "mer", "mer-1")
	if err := os.MkdirAll(rec.Metadata.WorkspacePath, 0o750); err != nil {
		t.Fatal(err)
	}
	st.sessions[rec.ID] = rec
	project := st.projects["mer"]
	project.Path = t.TempDir()
	project.Config.PreRemove = []string{command}
	st.projects["mer"] = project
	return m, st, ws, rec
}

func waitingCleanupCommand() string {
	if runtime.GOOS == "windows" {
		return "echo started > cleanup-started & for /L %i in (1,0,2) do @if exist cleanup-release exit /b 0"
	}
	return "echo started > cleanup-started; while [ ! -f cleanup-release ]; do sleep 0.05; done"
}

func waitForCleanupStart(t *testing.T, workspace string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(workspace, "cleanup-started")); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cleanup command did not start")
}

type notifyingCleanupLifecycle struct {
	lifecycleRecorder
	terminated chan struct{}
}

func (l notifyingCleanupLifecycle) MarkTerminated(ctx context.Context, id domain.SessionID) error {
	err := l.lifecycleRecorder.MarkTerminated(ctx, id)
	if err == nil {
		select {
		case l.terminated <- struct{}{}:
		default:
		}
	}
	return err
}

func TestKillCleanupStopsOnDaemonShutdownWithTerminalIntentSaved(t *testing.T) {
	m, st, ws, rec := newCleanupFixture(t, waitingCleanupCommand())
	daemonCtx, stop := context.WithCancel(context.Background())
	defer stop()
	m.backgroundContext = daemonCtx
	terminated := make(chan struct{}, 1)
	m.lcm = notifyingCleanupLifecycle{m.lcm, terminated}
	st.worktrees[rec.ID] = []domain.SessionWorktreeRecord{{SessionID: rec.ID, RepoName: domain.RootWorkspaceRepoName}}
	done := make(chan error, 1)
	go func() {
		_, err := m.Kill(context.Background(), rec.ID)
		done <- err
	}()
	select {
	case <-terminated:
	case <-time.After(10 * time.Second):
		t.Fatal("Kill did not persist terminal intent")
	}
	if !st.sessions[rec.ID].IsTerminated || len(st.worktrees[rec.ID]) != 0 {
		t.Fatal("terminal intent and restore-marker removal must precede cleanup")
	}
	waitForCleanupStart(t, rec.Metadata.WorkspacePath)
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrCleanupScript) || ws.destroyed != 0 {
			t.Fatalf("shutdown must cancel cleanup and preserve the workspace: err=%v destroyed=%d", err, ws.destroyed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon shutdown did not release cleanup")
	}
	if err := m.beginAgentOperation(context.Background(), rec.ID, agentOperationRestore); err != nil {
		t.Fatalf("shutdown left the session operation locked: %v", err)
	}
	m.endAgentOperation(rec.ID, agentOperationRestore)
}

func TestCleanupReleasesProjectGateAndRechecksOwnership(t *testing.T) {
	m, st, ws, rec := newCleanupFixture(t, waitingCleanupCommand())
	rec.IsTerminated = true
	rec.Metadata.RuntimeHandleID = ""
	st.sessions[rec.ID] = rec
	daemonCtx, stop := context.WithCancel(context.Background())
	defer stop()
	m.backgroundContext = daemonCtx
	done := make(chan CleanupResult, 1)
	go func() {
		result, err := m.Cleanup(context.Background(), rec.ProjectID)
		if err != nil {
			t.Errorf("cleanup: %v", err)
		}
		done <- result
	}()
	waitForCleanupStart(t, rec.Metadata.WorkspacePath)
	retry, err := m.Cleanup(context.Background(), rec.ProjectID)
	if err != nil || len(retry.Skipped) != 1 || retry.Skipped[0].Reason != "workspace cleanup is already running" {
		t.Fatalf("concurrent retry must not start another script: result=%+v err=%v", retry, err)
	}
	gate := make(chan func(), 1)
	go func() { gate <- m.acquireWorkspaceGate(rec.ProjectID) }()
	select {
	case release := <-gate:
		// Model a different session claiming this worktree while the script runs.
		successor := mkLive("mer-2")
		successor.Metadata.WorkspacePath = rec.Metadata.WorkspacePath
		st.sessions[successor.ID] = successor
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup held the project gate while running a command")
	}
	if err := os.WriteFile(filepath.Join(rec.Metadata.WorkspacePath, "cleanup-release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if len(result.Skipped) != 1 || result.Skipped[0].Reason != "workspace in use by a live session" || ws.destroyed != 0 {
			t.Fatalf("cleanup must recheck ownership before removal: result=%+v destroyed=%d", result, ws.destroyed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cleanup did not finish")
	}
}

func TestSpawnRollbackReleasesProjectGateBeforeCleanup(t *testing.T) {
	m, _, ws, rec := newCleanupFixture(t, waitingCleanupCommand())
	daemonCtx, stop := context.WithCancel(context.Background())
	defer stop()
	m.backgroundContext = daemonCtx
	release := sync.OnceFunc(m.acquireWorkspaceGate(rec.ProjectID))
	defer release()
	ctx := context.WithValue(context.Background(), spawnWorkspaceGateKey{}, release)
	done := make(chan bool, 1)
	go func() { done <- m.destroySpawnWorkspace(ctx, workspaceInfo(rec), nil) }()
	waitForCleanupStart(t, rec.Metadata.WorkspacePath)
	gate := make(chan func(), 1)
	go func() { gate <- m.acquireWorkspaceGate(rec.ProjectID) }()
	select {
	case release := <-gate:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("spawn rollback held the project gate while running cleanup")
	}
	stop()
	select {
	case removed := <-done:
		if removed || ws.destroyed != 0 {
			t.Fatal("cancelled rollback cleanup must preserve the workspace")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not release rollback cleanup")
	}
}

func TestCleanupReportsOnlyStepAndExitStatus(t *testing.T) {
	t.Setenv("QA_DAEMON_TOKEN", "inherited-secret")
	command := "echo $QA_DAEMON_TOKEN /private/project/file abcdef; exit 7"
	if runtime.GOOS == "windows" {
		command = "echo %QA_DAEMON_TOKEN% C:\\private\\project\\file abcdef & exit /b 7"
	}
	m, st, _, rec := newCleanupFixture(t, command)
	rec.IsTerminated = true
	st.sessions[rec.ID] = rec
	project := st.projects["mer"]
	project.Config.Env = map[string]string{"A": "abc", "B": "abcdef"}
	st.projects["mer"] = project
	var log bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&log, nil))
	result, err := m.Cleanup(context.Background(), rec.ProjectID)
	if err != nil || len(result.Skipped) != 1 || result.Skipped[0].Reason != "cleanup step 1 failed: exit status 7" {
		t.Fatalf("unsafe cleanup report: result=%+v err=%v", result, err)
	}
	if strings.Contains(log.String(), "abcdef") || strings.Contains(log.String(), "[REDACTED]def") || !strings.Contains(log.String(), "inherited-secret") {
		t.Fatalf("output must stay in the log with longest configured values redacted first: %s", log.String())
	}
	if got := cleanupSkipReason(errors.Join(ErrCleanupScript, errors.New("secret output"))); got != "workspace cleanup script failed" {
		t.Fatalf("untyped cleanup errors must not expose their message: %q", got)
	}
}

func TestCleanupFiltersReservedProjectEnvironment(t *testing.T) {
	t.Setenv("AO_TEST_RESERVED", "inherited")
	command := "printf '%s\\n' \"$AO_TEST_RESERVED\" \"$AO_WORKTREE_PATH\" \"$PROJECT_VALUE\" > env-check"
	if runtime.GOOS == "windows" {
		command = "(echo %AO_TEST_RESERVED%& echo %AO_WORKTREE_PATH%& echo %PROJECT_VALUE%) > env-check"
	}
	m, st, _, rec := newCleanupFixture(t, command)
	project := st.projects["mer"]
	project.Config.Env = map[string]string{"AO_TEST_RESERVED": "spoof", "AO_WORKTREE_PATH": "spoof", "PROJECT_VALUE": "project"}
	st.projects["mer"] = project
	if err := m.runPreRemove(rec.ProjectID, rec.Metadata.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(rec.Metadata.WorkspacePath, "env-check"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")), "\n")
	if len(got) != 3 || got[0] != "inherited" || got[1] != rec.Metadata.WorkspacePath || got[2] != "project" {
		t.Fatalf("reserved project variables were not filtered: %q", data)
	}
}

func TestWorkspaceStepBoundsOrphanedOutputPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX background process syntax")
	}
	started := time.Now()
	err := runWorkspaceStep(context.Background(), t.TempDir(), "sleep 3 &", nil, io.Discard)
	if err != nil || time.Since(started) >= 3*time.Second {
		t.Fatalf("orphaned pipe must not wait for the background child: err=%v duration=%v", err, time.Since(started))
	}
}

func TestRetireForReplacementRemovesWorkspaceWhenCleanupFails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
	}{
		{name: "failing script", command: "exit 7"},
		{name: "hung script", command: waitingCleanupCommand()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := replacementCleanupBudget
			replacementCleanupBudget = 500 * time.Millisecond
			t.Cleanup(func() { replacementCleanupBudget = previous })
			m, st, ws, rec := newCleanupFixture(t, tc.command)
			rec.Kind = domain.KindOrchestrator
			st.sessions[rec.ID] = rec
			st.worktrees[rec.ID] = []domain.SessionWorktreeRecord{{SessionID: rec.ID, RepoName: domain.RootWorkspaceRepoName}}
			started := time.Now()
			if err := m.RetireForReplacement(context.Background(), rec.ID); err != nil {
				t.Fatalf("replacement must not depend on cleanup scripts: %v", err)
			}
			if elapsed := time.Since(started); elapsed > 10*time.Second {
				t.Fatalf("replacement waited %v for cleanup", elapsed)
			}
			if !slices.Contains(ws.calls, "ForceDestroy:"+string(rec.ID)) {
				t.Fatalf("replacement did not force-remove the workspace: %v", ws.calls)
			}
			if !st.sessions[rec.ID].IsTerminated || len(st.worktrees[rec.ID]) != 0 {
				t.Fatal("retired orchestrator must be terminated without a restore marker")
			}
		})
	}
}

func TestWorkspaceStepDoesNotHideFailureWithDetachedChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX background syntax")
	}
	err := runWorkspaceStep(context.Background(), t.TempDir(), "sleep 3 & exit 7", nil, io.Discard)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("lost foreground failure: %v", err)
	}
}

func TestRequestKillReturnsBeforeCleanupAndShutdownPreservesWorkspace(t *testing.T) {
	m, st, ws, rec := newCleanupFixture(t, waitingCleanupCommand())
	daemonCtx, stop := context.WithCancel(context.Background())
	defer stop()
	m.backgroundContext = daemonCtx
	st.worktrees[rec.ID] = []domain.SessionWorktreeRecord{{SessionID: rec.ID, RepoName: domain.RootWorkspaceRepoName}}
	result, err := m.RequestKill(context.Background(), rec.ID)
	if err != nil || !result.CleanupPending || result.Freed || !st.sessions[rec.ID].IsTerminated || len(st.worktrees[rec.ID]) != 0 {
		t.Fatalf("Kill must acknowledge terminal intent before cleanup: result=%+v err=%v", result, err)
	}
	waitForCleanupStart(t, rec.Metadata.WorkspacePath)
	facts, ok, err := st.GetSessionCleanupFacts(context.Background(), rec.ID)
	if err != nil || !ok || facts.WorkspaceDisposition != domain.DispositionPending {
		t.Fatalf("pending cleanup not recorded: %+v %v", facts, err)
	}
	if _, err := m.RestoreWithMode(context.Background(), rec.ID); !errors.Is(err, ErrSwitchInProgress) {
		t.Fatalf("restore must not overlap cleanup: %v", err)
	}
	retry, err := m.Cleanup(context.Background(), rec.ProjectID)
	if err != nil || len(retry.Skipped) != 1 || retry.Skipped[0].Reason != "workspace cleanup is already running" {
		t.Fatalf("duplicate cleanup ran: %+v %v", retry, err)
	}
	gate := make(chan func(), 1)
	go func() { gate <- m.acquireWorkspaceGate(rec.ProjectID) }()
	select {
	case release := <-gate:
		release()
	case <-time.After(time.Second):
		t.Fatal("background cleanup blocked the project")
	}
	stop()
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.WaitBackgroundWorkers(waitCtx); err != nil {
		t.Fatal(err)
	}
	facts, _, _ = st.GetSessionCleanupFacts(context.Background(), rec.ID)
	if facts.WorkspaceDisposition != domain.DispositionFailed || ws.destroyed != 0 {
		t.Fatalf("shutdown must preserve the worktree and record failure: facts=%+v destroyed=%d", facts, ws.destroyed)
	}
	if err := m.beginAgentOperation(context.Background(), rec.ID, agentOperationRestore); err != nil {
		t.Fatal(err)
	}
	m.endAgentOperation(rec.ID, agentOperationRestore)
}

func TestRequestKillCleanupFailureRemainsRetryable(t *testing.T) {
	m, st, ws, rec := newCleanupFixture(t, "echo inherited-secret && exit 7")
	var jobs []func()
	m.runBackground = func(work func()) { jobs = append(jobs, work) }
	result, err := m.RequestKill(context.Background(), rec.ID)
	if err != nil || !result.CleanupPending || len(jobs) != 1 {
		t.Fatalf("not accepted: %+v %v", result, err)
	}
	jobs[0]()
	facts, _, _ := st.GetSessionCleanupFacts(context.Background(), rec.ID)
	if facts.WorkspaceDisposition != domain.DispositionFailed || facts.FailureCode != "WORKSPACE_CLEANUP_FAILED" || facts.AttemptCount != 1 || ws.destroyed != 0 {
		t.Fatalf("background failure lost or output persisted: %+v destroyed=%d", facts, ws.destroyed)
	}
	project := st.projects[string(rec.ProjectID)]
	project.Config.PreRemove = []string{"echo fixed"}
	st.projects[string(rec.ProjectID)] = project
	retry, err := m.Cleanup(context.Background(), rec.ProjectID)
	facts, _, _ = st.GetSessionCleanupFacts(context.Background(), rec.ID)
	if err != nil || len(retry.Cleaned) != 1 || facts.WorkspaceDisposition != domain.DispositionRemoved || facts.FailureCode != "" || facts.AttemptCount != 2 {
		t.Fatalf("retry did not clear the failure: result=%+v facts=%+v err=%v", retry, facts, err)
	}
}
