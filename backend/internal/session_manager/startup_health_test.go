package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type healthCheckLauncher struct {
	recordingLauncher
	checks []ChatStart
	err    error
}

func (l *healthCheckLauncher) StartChat(_ context.Context, cfg ChatStart) (ChatStarted, error) {
	l.checks = append(l.checks, cfg)
	return ChatStarted{}, l.err
}

func TestStartupHealthNeverLaunchesStoppedSessions(t *testing.T) {
	ctx := context.Background()
	m, st, rt, ws := newLifecycleManager()
	launcher := &healthCheckLauncher{err: ports.ErrChatHostNotRunning}
	m.chat = launcher
	st.sessions["mer-chat"] = domain.SessionRecord{
		ID: "mer-chat", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/chat", Branch: "ao/chat/root", ProviderConversationID: "thread-1"},
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
	st.sessions["mer-tui"] = domain.SessionRecord{
		ID: "mer-tui", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/tui", Branch: "ao/tui/root", RuntimeHandleID: "dead"},
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
	st.sessions["mer-saved"] = domain.SessionRecord{
		ID: "mer-saved", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/saved", Branch: "ao/saved/root", AgentSessionID: "native"},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-saved"] = []domain.SessionWorktreeRecord{{SessionID: "mer-saved", RepoName: domain.RootWorkspaceRepoName, State: "removed"}}
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if rt.created != 0 || len(ws.restoreConfigs) != 0 {
		t.Fatalf("startup launched runtime or restored workspace: runtimes=%d restores=%d", rt.created, len(ws.restoreConfigs))
	}
	if len(launcher.checks) != 1 || !launcher.checks[0].ReconnectOnly {
		t.Fatalf("startup did not enforce reconnect-only: %+v", launcher.checks)
	}
	for _, id := range []domain.SessionID{"mer-chat", "mer-tui"} {
		if st.sessions[id].Activity.State != domain.ActivityExited || st.sessions[id].IsTerminated {
			t.Fatalf("%s must stay stopped and resumable: %+v", id, st.sessions[id])
		}
	}
	if !st.sessions["mer-saved"].IsTerminated || len(st.worktrees["mer-saved"]) != 1 {
		t.Fatal("startup changed a shutdown-saved session or its restore marker")
	}
}

func TestStartupHealthInconclusiveChatProbePreservesSession(t *testing.T) {
	m, st, rt, ws := newLifecycleManager()
	launcher := &healthCheckLauncher{err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	rec := domain.SessionRecord{ID: "mer-chat", ProjectID: "mer", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Metadata: domain.SessionMetadata{WorkspacePath: "/ws/chat", Branch: "ao/chat/root", ProviderConversationID: "thread-1"}, Activity: domain.Activity{State: domain.ActivityActive}}
	st.sessions[rec.ID] = rec
	if err := m.checkSessionHealth(context.Background(), rec); !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("error=%v", err)
	}
	if st.sessions[rec.ID].Activity.State != domain.ActivityActive || rt.created != 0 || len(ws.restoreConfigs) != 0 {
		t.Fatal("uncertain probe changed activity or launched resources")
	}
}

func TestStartupHealthUnavailableRuntimeProbePreservesSession(t *testing.T) {
	m, st, rt, ws := newLifecycleManager()
	rt.aliveErr = ports.ErrRuntimeUnavailable
	rec := domain.SessionRecord{
		ID: "mer-tui", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/tui", Branch: "ao/tui/root", RuntimeHandleID: "tmux-1"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[rec.ID] = rec

	err := m.checkSessionHealth(context.Background(), rec)
	if !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("error=%v, want runtime unavailable", err)
	}
	if st.sessions[rec.ID].Activity.State != domain.ActivityActive || rt.created != 0 || len(ws.restoreConfigs) != 0 {
		t.Fatal("unavailable runtime probe changed activity or launched resources")
	}
}

// flakyStartupLauncher fails reattach attempts until failures runs out, then
// reports a live controller the way a successful reconnect does.
type flakyStartupLauncher struct {
	recordingLauncher
	mu       sync.Mutex
	failures int
	err      error
	attempts []ChatStart
	attached bool
}

func (l *flakyStartupLauncher) StartChat(_ context.Context, cfg ChatStart) (ChatStarted, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts = append(l.attempts, cfg)
	if l.failures != 0 {
		l.failures--
		return ChatStarted{}, l.err
	}
	l.attached = true
	return ChatStarted{}, nil
}

func (l *flakyStartupLauncher) HasLiveChatController(domain.SessionID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.attached
}

func (l *flakyStartupLauncher) attemptCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.attempts)
}

func (l *flakyStartupLauncher) setAttached() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attached = true
}

func startupChatRecord() domain.SessionRecord {
	return domain.SessionRecord{
		ID: "mer-chat", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/chat", Branch: "ao/chat/root", ProviderConversationID: "thread-1"},
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStartupChatCheckRetriesTransientFailure(t *testing.T) {
	for name, cause := range map[string]error{
		// The production failure: the check spent its deadline waiting for the writer.
		"deadline":     fmt.Errorf("reconcile conversation authentication: %w", context.DeadlineExceeded),
		"inconclusive": fmt.Errorf("persistent host: %w", ports.ErrChatRecoveryInconclusive),
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m, st, rt, ws := newLifecycleManager()
			launcher := &flakyStartupLauncher{failures: 2, err: cause}
			m.chat = launcher
			m.startupChatRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
			rec := startupChatRecord()
			st.sessions[rec.ID] = rec
			if err := m.ReconcileBackground(ctx); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the retry to reattach", func() bool { return launcher.HasLiveChatController(rec.ID) })
			waitFor(t, "ready readiness", func() bool { return m.SessionStatusReadiness(st.sessions[rec.ID]) == "ready" })
			if got := launcher.attemptCount(); got != 3 {
				t.Fatalf("attempts = %d, want the startup check plus two retries", got)
			}
			for i, attempt := range launcher.attempts {
				if !attempt.ReconnectOnly {
					t.Fatalf("attempt %d may launch a provider: %+v", i, attempt)
				}
			}
			if rt.created != 0 || len(ws.restoreConfigs) != 0 || st.sessions[rec.ID].Activity != rec.Activity {
				t.Fatal("retry launched resources or changed activity")
			}
		})
	}
}

func TestStartupChatCheckDoesNotRetryPermanentFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: -1, err: errors.New("provider rejected the permission mode")}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{time.Millisecond}
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := launcher.attemptCount(); got != 1 {
		t.Fatalf("attempts = %d, want no retry", got)
	}
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "unavailable" {
		t.Fatalf("readiness = %s, want unavailable so Resume is offered", got)
	}
}

func TestStartupChatCheckRetryStopsOnceReattached(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: -1, err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{20 * time.Millisecond, time.Millisecond}
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	launcher.setAttached() // Resume reconnected before the first retry.
	time.Sleep(80 * time.Millisecond)
	if got := launcher.attemptCount(); got != 1 {
		t.Fatalf("attempts = %d, want the retry to leave a reattached chat alone", got)
	}
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "ready" {
		t.Fatalf("readiness = %s, want the reattached chat settled as ready", got)
	}
}

func TestStartupChatCheckStaysCheckingWhileRetryPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: 1, err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{100 * time.Millisecond}
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	// The startup attempt failed, but a reattach is scheduled: clients must show
	// a reconnect in progress, not offer Resume.
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "checking" {
		t.Fatalf("readiness while a retry is pending = %s, want checking", got)
	}
	waitFor(t, "the retry to reattach", func() bool { return launcher.HasLiveChatController(rec.ID) })
	waitFor(t, "ready readiness", func() bool { return m.SessionStatusReadiness(st.sessions[rec.ID]) == "ready" })
}

func TestStartupChatCheckRetryGivesUpAfterSchedule(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: -1, err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "every scheduled retry", func() bool { return launcher.attemptCount() == 3 })
	time.Sleep(20 * time.Millisecond)
	if got := launcher.attemptCount(); got != 3 {
		t.Fatalf("attempts = %d, want one per scheduled delay", got)
	}
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "unavailable" {
		t.Fatalf("readiness = %s, want unavailable so Resume is offered", got)
	}
}

// Builds before background branch naming left this state; it is still rolled back.
func TestStartupHealthCleansOnlyUncommittedSetup(t *testing.T) {
	for _, scenario := range []string{"clean", "dirty", "committed", "runtime", "provider", "setup-done"} {
		t.Run(scenario, func(t *testing.T) {
			m, st, ws, _ := newGitTaskPreparationManager(t)
			rec, err := st.CreateSession(t.Context(), domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, Mode: domain.SessionModeTUI, Harness: domain.HarnessClaudeCode, ClientRequestID: "create-worker", ProvisionSteps: []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepSetup, Status: domain.SessionProvisionStepRunning}}})
			if err != nil {
				t.Fatal(err)
			}
			root, err := ws.Create(t.Context(), ports.WorkspaceConfig{ProjectID: "mer", SessionID: rec.ID, Branch: "ao/login/root", BaseBranch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			rec.Metadata.WorkspacePath, rec.Metadata.Branch, rec.Metadata.WorkspaceRepoPath = root.Path, root.Branch, root.RepoPath
			switch scenario {
			case "dirty":
				if err := os.WriteFile(filepath.Join(root.Path, "keep"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "committed":
				rec.ClientRequestCommitted = true
			case "runtime":
				rec.Metadata.RuntimeHandleID = "live-runtime"
			case "provider":
				rec.Metadata.ProviderConversationID = "live-provider"
			case "setup-done":
				rec.ProvisionSteps[0].Status = domain.SessionProvisionStepDone
			}
			st.sessions[rec.ID] = rec
			_ = m.checkSessionHealth(t.Context(), rec)
			_, found := st.sessions[rec.ID]
			_, pathErr := os.Stat(root.Path)
			if scenario == "clean" {
				if found || !errors.Is(pathErr, os.ErrNotExist) {
					t.Fatalf("unfinished clean request remains: found=%v path=%v", found, pathErr)
				}
			} else if !found || pathErr != nil {
				t.Fatalf("protected request or workspace lost: found=%v path=%v", found, pathErr)
			}
		})
	}
}
