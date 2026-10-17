//go:build darwin || linux

package conpty

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// runTestSupervisor mirrors `ao agent-process supervise`'s process shape: it
// owns the managed agent as its only child and stays alive after that child
// exits until its exit report completes. Here the report is a file barrier the
// test controls, so the window is ordered without guessing timing. An optional
// start gate holds the supervisor alive before it starts its agent.
func runTestSupervisor(args []string) int {
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		return 2
	}
	if gate := os.Getenv("AO_TEST_SUPERVISOR_START_GATE"); gate != "" {
		_ = os.WriteFile(os.Getenv("AO_TEST_SUPERVISOR_STARTING"), []byte("starting"), 0o600)
		awaitTestFile(gate)
	}
	child := exec.Command(args[1], args[2:]...) //nolint:forbidigo // test stand-in for the AO supervisor
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		return 1
	}
	_ = child.Wait()
	_ = os.WriteFile(os.Getenv("AO_TEST_SUPERVISOR_REPORTING"), []byte("reporting"), 0o600)
	awaitTestFile(os.Getenv("AO_TEST_SUPERVISOR_RELEASE"))
	return 0
}

func awaitTestFile(path string) {
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const supervisedWindowAgent = `printf ready > "$AO_TEST_AGENT_READY"
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$AO_TEST_AGENT_INPUT"
  if [ "$line" = "__exit__" ]; then exit 0; fi
done`

type supervisedWindow struct {
	rt        *Runtime
	handle    ports.RuntimeHandle
	ref       ports.FencedRuntimeRef
	exact     ports.SupervisedProcessRef
	input     string
	reporting string
	release   string
	starting  string
	startGate string
}

func startSupervisedWindow(t *testing.T, sessionID, launchID string, supervised bool) *supervisedWindow {
	t.Helper()
	w := createSupervisedWindow(t, sessionID, launchID, supervised, false)
	waitForTestFile(t, filepath.Join(filepath.Dir(w.input), "agent.ready"), "ready")
	return w
}

// createSupervisedWindow starts the host without waiting for the agent. With
// gateStart, the supervisor stays alive without an agent until releaseStart.
func createSupervisedWindow(t *testing.T, sessionID, launchID string, supervised, gateStart bool) *supervisedWindow {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	w := &supervisedWindow{
		rt:        New(Options{RunFilePath: filepath.Join(dir, "running.json")}),
		input:     filepath.Join(dir, "agent.input"),
		reporting: filepath.Join(dir, "supervisor.reporting"),
		release:   filepath.Join(dir, "supervisor.release"),
		starting:  filepath.Join(dir, "supervisor.starting"),
	}
	if gateStart {
		w.startGate = filepath.Join(dir, "supervisor.start")
	}
	argv := []string{"/bin/sh", "-c", supervisedWindowAgent}
	if supervised {
		argv = append([]string{exe, "agent-process", "supervise", "--session", sessionID, "--launch", launchID, "--"}, argv...)
	}
	ready := filepath.Join(dir, "agent.ready")
	w.handle, err = w.rt.Create(context.Background(), ports.RuntimeConfig{
		SessionID: domain.SessionID(sessionID), WorkspacePath: dir, Argv: argv,
		Env: map[string]string{
			runtimeLaunchIDEnv:              launchID,
			"AO_TEST_AGENT_READY":           ready,
			"AO_TEST_AGENT_INPUT":           w.input,
			"AO_TEST_SUPERVISOR_REPORTING":  w.reporting,
			"AO_TEST_SUPERVISOR_RELEASE":    w.release,
			"AO_TEST_SUPERVISOR_STARTING":   w.starting,
			"AO_TEST_SUPERVISOR_START_GATE": w.startGate,
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		if w.startGate != "" {
			_ = os.WriteFile(w.startGate, nil, 0o600)
		}
		_ = os.WriteFile(w.release, nil, 0o600)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = w.rt.Destroy(ctx, w.handle)
	})
	w.ref = ports.FencedRuntimeRef{Handle: w.handle, SessionID: domain.SessionID(sessionID), Generation: launchID}
	w.exact = ports.SupervisedProcessRef{SessionID: domain.SessionID(sessionID), LaunchID: launchID}
	return w
}

func (w *supervisedWindow) releaseStart(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(w.startGate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, filepath.Join(filepath.Dir(w.input), "agent.ready"), "ready")
}

func (w *supervisedWindow) exitAgent(t *testing.T) {
	t.Helper()
	if err := w.rt.SendInput(context.Background(), w.handle, "__exit__\r"); err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, w.input, "__exit__")
}

func waitForTestFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, _ := os.ReadFile(path)
		if strings.Contains(string(raw), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in %s; got %q", want, path, raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// T02 adapter fixture: real native host and PTY, with the supervisor held in
// its exit-report window after the managed agent exits.
func TestExactProbeRejectsSupervisorReportingExitedChild(t *testing.T) {
	w := startSupervisedWindow(t, "t02-fixture", "launch-1", true)
	ctx := context.Background()
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedAlive {
		t.Fatalf("live supervised agent = %+v, want alive", probe)
	}
	if alive, err := w.rt.IsExactSupervisedProcessAlive(ctx, w.handle, w.exact); err != nil || !alive {
		t.Fatalf("live supervised agent exact = %v, %v; want alive", alive, err)
	}

	w.exitAgent(t)
	waitForTestFile(t, w.reporting, "reporting")
	if alive, err := w.rt.IsChildAlive(ctx, w.handle); err != nil || !alive {
		t.Fatalf("precondition: supervisor must be alive while reporting: %v, %v", alive, err)
	}
	// The ordinary reaper probe intentionally keeps reporting the supervisor.
	if alive, err := w.rt.IsSupervisedProcessAlive(ctx, w.handle, w.exact); err != nil || !alive {
		t.Fatalf("ordinary supervised probe = %v, %v; want unchanged alive", alive, err)
	}
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedDead || probe.Reason != ports.FencedReasonExactAbsent {
		t.Fatalf("fenced probe during report window = %+v, want dead/exact_absent", probe)
	}
	if alive, err := w.rt.IsExactSupervisedProcessAlive(ctx, w.handle, w.exact); err != nil || alive {
		t.Fatalf("exact probe during report window = %v, %v; want definitely not alive", alive, err)
	}
	// A different generation is never confirmed and never reported dead.
	if probe := w.rt.ProbeFencedRuntime(ctx, ports.FencedRuntimeRef{Handle: w.handle, SessionID: w.ref.SessionID, Generation: "launch-0"}); probe.Liveness != ports.FencedUnknown || probe.Reason != ports.FencedReasonGenerationMismatch {
		t.Fatalf("old generation probe = %+v, want unknown/generation_mismatch", probe)
	}
}

// An unsupervised hook-native agent is the host's direct child, so its own
// status remains the exact evidence; no process-table inspection applies.
func TestExactProbeUsesDirectChildForUnsupervisedLaunch(t *testing.T) {
	w := startSupervisedWindow(t, "t02-direct", "launch-1", false)
	ctx := context.Background()
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedAlive {
		t.Fatalf("live direct agent = %+v, want alive", probe)
	}
	w.exitAgent(t)
	deadline := time.Now().Add(5 * time.Second)
	for {
		probe := w.rt.ProbeFencedRuntime(ctx, w.ref)
		if probe.Liveness == ports.FencedDead {
			break
		}
		if probe.Liveness == ports.FencedAlive && time.Now().After(deadline) {
			t.Fatalf("direct agent exit never observed: %+v", probe)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// T05 (native): when the process table cannot be read, a supervised launch is
// unknown rather than alive or dead.
func TestExactProbeTreatsUnreadableProcessTableAsUnknown(t *testing.T) {
	w := startSupervisedWindow(t, "t05-native", "launch-1", true)
	w.rt.processTable = func(context.Context) (*procmem.Table, error) {
		return nil, errors.New("ps: permission denied")
	}
	ctx := context.Background()
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedUnknown || probe.Reason != ports.FencedReasonProbeFailed {
		t.Fatalf("fenced probe = %+v, want unknown/probe_failed", probe)
	}
	if alive, err := w.rt.IsExactSupervisedProcessAlive(ctx, w.handle, w.exact); err == nil || alive || !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
		t.Fatalf("exact probe = %v, %v; want inconclusive error", alive, err)
	}
	// The ordinary reaper probe never consults the process table.
	if alive, err := w.rt.IsSupervisedProcessAlive(ctx, w.handle, w.exact); err != nil || !alive {
		t.Fatalf("ordinary supervised probe = %v, %v; want alive", alive, err)
	}
}

// A daemon restart recovers the supervised flag from the registry, so the
// report window is still not mistaken for a live agent. The restarted daemon
// never saw the agent, and a supervisor without a child is exactly what a
// starting launch looks like, so the window is unconfirmed rather than dead
// until the supervisor itself exits.
func TestSupervisedFlagSurvivesRegistryRecovery(t *testing.T) {
	w := startSupervisedWindow(t, "t02-recovered", "launch-1", true)
	w.exitAgent(t)
	waitForTestFile(t, w.reporting, "reporting")
	ctx := context.Background()
	recovered := New(Options{RunFilePath: filepath.Join(filepath.Dir(w.input), "running.json")})
	if probe := recovered.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedUnknown || probe.Reason != ports.FencedReasonWorkloadUnconfirmed {
		t.Fatalf("recovered probe during report window = %+v, want unknown/workload_unconfirmed", probe)
	}
	if alive, err := recovered.IsExactSupervisedProcessAlive(ctx, w.handle, w.exact); alive || !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
		t.Fatalf("recovered exact probe during report window = %v, %v; want inconclusive", alive, err)
	}
	if err := os.WriteFile(w.release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForFencedLiveness(t, recovered, w.ref, ports.FencedDead)
}

func waitForFencedLiveness(t *testing.T, rt *Runtime, ref ports.FencedRuntimeRef, want ports.FencedLiveness) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		probe := rt.ProbeFencedRuntime(context.Background(), ref)
		if probe.Liveness == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe = %+v, want %s", probe, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A healthy supervisor exists before it starts its agent. That state is
// unconfirmed, never a definite exit: definite-exit callers send AGENT_EXITED
// and agent-switch recovery destroys and rolls back on it.
func TestExactProbeTreatsStartingSupervisorAsUnconfirmed(t *testing.T) {
	w := createSupervisedWindow(t, "startup-native", "launch-1", true, true)
	waitForTestFile(t, w.starting, "starting")
	ctx := context.Background()
	if alive, err := w.rt.IsChildAlive(ctx, w.handle); err != nil || !alive {
		t.Fatalf("precondition: supervisor must be alive before starting its agent: %v, %v", alive, err)
	}
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedUnknown || probe.Reason != ports.FencedReasonWorkloadUnconfirmed {
		t.Fatalf("fenced probe before agent start = %+v, want unknown/workload_unconfirmed", probe)
	}
	if alive, err := w.rt.IsExactSupervisedProcessAlive(ctx, w.handle, w.exact); alive || !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
		t.Fatalf("exact probe before agent start = %v, %v; want inconclusive", alive, err)
	}
	if alive, err := w.rt.IsSupervisedProcessAlive(ctx, w.handle, w.exact); err != nil || !alive {
		t.Fatalf("ordinary supervised probe = %v, %v; want unchanged alive", alive, err)
	}

	w.releaseStart(t)
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedAlive {
		t.Fatalf("same launch after agent start = %+v, want alive", probe)
	}
	// Once the agent was seen, its absence is a confirmed exit.
	w.exitAgent(t)
	waitForTestFile(t, w.reporting, "reporting")
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedDead || probe.Reason != ports.FencedReasonExactAbsent {
		t.Fatalf("after observed agent exit = %+v, want dead/exact_absent", probe)
	}
}

// Concurrent probes can finish out of order. A probe whose real process
// snapshot was taken before the agent started must not be reinterpreted as an
// exit because a newer probe observed the agent while it was paused. Only the
// completion order of the snapshot reads is controlled; the snapshots are real.
func TestExactProbeStaleStartupSnapshotIsNotExit(t *testing.T) {
	w := createSupervisedWindow(t, "startup-concurrent", "launch-1", true, true)
	waitForTestFile(t, w.starting, "starting")
	snapshot := w.rt.processTable
	sampled := make(chan struct{})
	resume := make(chan struct{})
	var first sync.Once
	w.rt.processTable = func(ctx context.Context) (*procmem.Table, error) {
		table, err := snapshot(ctx)
		held := false
		first.Do(func() { held = true })
		if held {
			close(sampled)
			<-resume
		}
		return table, err
	}
	ctx := context.Background()
	stale := make(chan ports.FencedProbeResult, 1)
	go func() { stale <- w.rt.ProbeFencedRuntime(ctx, w.ref) }()
	<-sampled // the older probe holds a snapshot taken before child.Start

	w.releaseStart(t)
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedAlive {
		t.Fatalf("newer probe after agent start = %+v, want alive", probe)
	}
	close(resume)
	if probe := <-stale; probe.Liveness != ports.FencedUnknown || probe.Reason != ports.FencedReasonWorkloadUnconfirmed {
		t.Fatalf("older pre-start snapshot = %+v, want unknown/workload_unconfirmed", probe)
	}
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedAlive {
		t.Fatalf("the agent never exited, latest probe = %+v", probe)
	}
	// The same ordering through the exact probe: never a definite "not alive".
	exactSampled := make(chan struct{})
	exactResume := make(chan struct{})
	var exactFirst sync.Once
	w.rt.processTable = func(ctx context.Context) (*procmem.Table, error) {
		table, err := snapshot(ctx)
		held := false
		exactFirst.Do(func() { held = true })
		if held {
			close(exactSampled)
			<-exactResume
		}
		return table, err
	}
	// Exit the agent while an exact probe holds a snapshot taken while it ran:
	// a newer probe confirms the exit, and the older one still reports running.
	type exactResult struct {
		alive bool
		err   error
	}
	older := make(chan exactResult, 1)
	go func() {
		alive, err := w.rt.IsExactSupervisedProcessAlive(ctx, w.handle, w.exact)
		older <- exactResult{alive, err}
	}()
	<-exactSampled
	w.exitAgent(t)
	waitForTestFile(t, w.reporting, "reporting")
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedDead || probe.Reason != ports.FencedReasonExactAbsent {
		t.Fatalf("newer probe after observed exit = %+v, want dead/exact_absent", probe)
	}
	close(exactResume)
	if got := <-older; got.err != nil || !got.alive {
		t.Fatalf("older snapshot taken while running = %v, %v; want running at that moment", got.alive, got.err)
	}
}
