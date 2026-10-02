//go:build e2e && !windows

package sessionmanager

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type accountRetainedPaneAgent struct{ accountSwitchAgent }

func (a *accountRetainedPaneAgent) GetLaunchCommand(_ context.Context, cfg ports.LaunchConfig) ([]string, error) {
	a.lastLaunch = cfg
	return []string{"/bin/sh", "-c", `printf '%s' "$1"; sleep 30`, "workload", idleTerminalOutput}, nil
}

func (*accountRetainedPaneAgent) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	return (transitionSurfaceAgent{}).InspectTerminalSurface(strings.TrimSpace(output))
}

func realAccountRuntimeFixture(t *testing.T) (*Manager, *sqlite.Store, *tmux.Runtime, *accountRetainedPaneAgent, domain.SessionRecord, AccountsManagerSwitchConfig) {
	t.Helper()
	tmuxBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "account-switch-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	binary := filepath.Join(t.TempDir(), "ao")
	build := exec.CommandContext(t.Context(), "go", "build", "-mod=readonly", "-p=1", "-o", binary, "../../cmd/ao")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build isolated supervisor: %v\n%s", err, out)
	}
	m, st, _, _, rec, cfg := accountSwitchFixture(t)
	m.runFilePath = filepath.Join(m.dataDir, "unused-running.json")
	runtime := tmux.New(tmux.Options{Binary: tmuxBinary, LegacyBinary: tmuxBinary, SocketName: "switch-residue", Shell: "/bin/sh", Timeout: time.Second})
	ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: ports.RuntimeHandle{ID: string(rec.ID)}, Generation: rec.Metadata.RuntimeLaunchID}
	t.Cleanup(func() { _ = runtime.Destroy(context.Background(), ref.Handle) })
	if _, err := runtime.Create(t.Context(), ports.RuntimeConfig{SessionID: rec.ID, WorkspacePath: rec.Metadata.WorkspacePath,
		Argv: []string{binary, "agent-process", "supervise", "--session", string(rec.ID), "--launch", ref.Generation, "--", "/bin/true"},
		Env:  map[string]string{EnvSupervisedProcess: "1", EnvRuntimeLaunchID: ref.Generation, EnvDataDir: m.dataDir, EnvRunFile: m.runFilePath}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		pane, err := exec.CommandContext(t.Context(), tmuxBinary, "-L", "switch-residue", "display-message", "-p", "-t", "="+string(rec.ID)+":", "#{pane_current_command}").Output()
		if err != nil {
			t.Fatal(err)
		}
		if string(pane) == "cat\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real supervisor never reached its retained pane")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.ProbeFencedRuntime(t.Context(), ref); got.Liveness != ports.FencedDead {
		t.Fatalf("retained source proof=%+v", got)
	}
	rec.Metadata.RuntimeHandleID = ref.Handle.ID
	if err := st.UpdateSession(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	agent := &accountRetainedPaneAgent{}
	m.runtime, m.agents = runtime, singleAgent{agent: agent}
	m.executable = func() (string, error) { return binary, nil }
	m.switchTargetStartWait = 3 * time.Second
	return m, st, runtime, agent, rec, cfg
}

func TestAccountsManagerSwitchRealExitedSupervisorPane(t *testing.T) {
	m, st, runtime, agent, rec, cfg := realAccountRuntimeFixture(t)
	ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: ports.RuntimeHandle{ID: string(rec.ID)}}
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	ready := waitAccountSwitch(t, m, st, cfg.OperationID)
	if ready.Phase != domain.AccountsManagerSwitchReady {
		ref.Generation = ready.TargetGeneration
		out, err := runtime.GetStyledOutput(t.Context(), ref.Handle, 5)
		t.Fatalf("real retained pane switch: phase=%s code=%s probe=%+v surface=%+v outputError=%v", ready.Phase, ready.ErrorCode, runtime.ProbeFencedRuntime(t.Context(), ref), agent.InspectTerminalSurface(out), err)
	}
	ref.Generation = ready.TargetGeneration
	if got := runtime.ProbeFencedRuntime(t.Context(), ref); got.Liveness != ports.FencedAlive {
		t.Fatalf("replacement process proof=%+v", got)
	}
	if agent.lastLaunch.Prompt != "" {
		t.Fatal("replacement replayed the saved task")
	}
}

type accountTmuxCleanupFailure struct {
	*tmux.Runtime
	cleanupFail bool
}

func (r *accountTmuxCleanupFailure) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	if r.cleanupFail {
		return errors.New("cleanup unavailable")
	}
	return r.Runtime.Destroy(ctx, handle)
}

type accountTmuxPublicationFailure struct {
	*lifecycle.Manager
	runtime *accountTmuxCleanupFailure
}

func (l *accountTmuxPublicationFailure) MarkSpawned(context.Context, domain.SessionID, domain.SessionMetadata) error {
	l.runtime.cleanupFail = true
	return errors.New("publication unavailable")
}

func TestAccountsManagerSwitchRealReservedTargetSurvivesColdRecovery(t *testing.T) {
	m, st, runtime, agent, rec, cfg := realAccountRuntimeFixture(t)
	m.switchTargetStartWait = time.Millisecond
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	first := waitAccountSwitch(t, m, st, cfg.OperationID)
	if first.Phase != domain.AccountsManagerSwitchRecoveryRequired {
		t.Fatal("fixture did not miss readiness")
	}
	rt := &accountTmuxCleanupFailure{Runtime: runtime}
	m.runtime = rt
	m.lcm = &accountTmuxPublicationFailure{Manager: lifecycle.New(st, nil), runtime: rt}
	if _, err := m.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
		t.Fatal(err)
	}
	failed := waitAccountSwitch(t, m, st, cfg.OperationID)
	if failed.TargetGeneration == first.TargetGeneration || failed.RetiredTargetGeneration != first.TargetGeneration {
		t.Fatal("fixture did not rotate ownership")
	}
	ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: ports.RuntimeHandle{ID: string(rec.ID)}, Generation: failed.TargetGeneration}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.ProbeFencedRuntime(t.Context(), ref).Liveness != ports.FencedAlive {
		if time.Now().After(deadline) {
			t.Fatal("unpublished target did not survive cleanup failure")
		}
		time.Sleep(10 * time.Millisecond)
	}
	retired := ref
	retired.Generation = first.TargetGeneration
	if got := runtime.ProbeFencedRuntime(t.Context(), retired); got.Reason != ports.FencedReasonGenerationMismatch {
		t.Fatalf("retired probe did not encounter reserved process: %+v", got)
	}
	project, err := m.loadProject(t.Context(), rec.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.OpenPreMigrated(project.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	binary, err := m.executable()
	if err != nil {
		t.Fatal(err)
	}
	tmuxBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	coldRuntime := tmux.New(tmux.Options{Binary: tmuxBinary, LegacyBinary: tmuxBinary, SocketName: "switch-residue", Shell: "/bin/sh", Timeout: time.Second})
	restarted := New(Deps{Store: reopened, Runtime: coldRuntime, Lifecycle: lifecycle.New(reopened, nil), AccountsManager: &accountSwitchRouter{store: reopened},
		Agents: singleAgent{agent: agent}, DataDir: m.dataDir, RunFilePath: m.runFilePath, LookPath: m.lookPath, BackgroundContext: t.Context()})
	restarted.executable = func() (string, error) { return binary, nil }
	restarted.switchTargetStartWait = 3 * time.Second
	if err := restarted.ReconcileStartupSafety(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RetryAccountsManagerSwitch(t.Context(), rec.ID, cfg.OperationID); err != nil {
		t.Fatal(err)
	}
	ready := waitAccountSwitch(t, restarted, reopened, cfg.OperationID)
	if ready.Phase != domain.AccountsManagerSwitchReady || ready.TargetRevision <= failed.TargetRevision {
		t.Fatalf("real cold recovery stranded: phase=%s code=%s", ready.Phase, ready.ErrorCode)
	}
	ref.Generation = ready.TargetGeneration
	if got := coldRuntime.ProbeFencedRuntime(t.Context(), ref); got.Liveness != ports.FencedAlive {
		t.Fatalf("real retry did not own the replacement: %+v", got)
	}
	if agent.lastLaunch.Prompt != "" {
		t.Fatal("real cold retry replayed the task")
	}
}
