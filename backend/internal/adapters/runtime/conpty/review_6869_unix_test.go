//go:build darwin || linux

package conpty

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A supervised launch carries its agent argv, and launch prompts can be argv.
// Text such as "<defunct>" in that argv is user content, not process state:
// it must not hide the live supervisor from the managed-workload probe.
func TestExactProbeLiveSupervisorWithDefunctInPrompt(t *testing.T) {
	w := createSupervisedWindow(t, "defunct-prompt", "launch-1", true, false, "explain <defunct> processes")
	waitForTestFile(t, filepath.Join(filepath.Dir(w.input), "agent.ready"), "ready")
	ctx := context.Background()
	if alive, err := w.rt.IsChildAlive(ctx, w.handle); err != nil || !alive {
		t.Fatalf("precondition: supervisor alive = %v, %v", alive, err)
	}
	if err := w.rt.SendInput(ctx, w.handle, "healthy\r"); err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, w.input, "healthy")
	for i := 0; i < 3; i++ {
		if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedAlive {
			t.Fatalf("probe %d of a healthy agent with <defunct> in its prompt = %+v, want alive", i+1, probe)
		}
	}
	if alive, err := w.rt.IsExactSupervisedProcessAlive(ctx, w.handle, w.exact); err != nil || !alive {
		t.Fatalf("exact probe = %v, %v; want alive", alive, err)
	}
}

// An agent that exited but is not yet reaped is a zombie. Its remaining
// process row must never be read as a running agent.
func TestExactProbeZombieWorkloadIsNotRunning(t *testing.T) {
	dir := t.TempDir()
	childPID := filepath.Join(dir, "agent.pid")
	t.Setenv("AO_TEST_SUPERVISOR_DEFER_REAP", "1")
	t.Setenv("AO_TEST_SUPERVISOR_CHILD_PID", childPID)
	w := startSupervisedWindow(t, "zombie-agent", "launch-1", true)
	ctx := context.Background()
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedAlive {
		t.Fatalf("running agent = %+v, want alive", probe)
	}
	raw, err := os.ReadFile(childPID)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		t.Fatalf("agent pid: %q, %v", raw, err)
	}
	w.exitAgent(t)
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := exec.Command("ps", "-o", "stat=", "-p", strings.TrimSpace(string(raw))).Output() //nolint:forbidigo // test inspects the real process state
		if strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent never became an unreaped zombie: %q", out)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if probe := w.rt.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedDead || probe.Reason != ports.FencedReasonExactAbsent {
		t.Fatalf("zombie agent = %+v, want dead/exact_absent", probe)
	}
	if alive, err := w.rt.IsExactSupervisedProcessAlive(ctx, w.handle, w.exact); err != nil || alive {
		t.Fatalf("exact probe of zombie agent = %v, %v; want not alive", alive, err)
	}
}

// rewriteAsPreUpgradeEntry rewrites a registry entry in the JSON layout
// written before supervision was recorded: the supervised key is absent and
// the host PID, address, and launch id are preserved.
func rewriteAsPreUpgradeEntry(t *testing.T, w *supervisedWindow) string {
	t.Helper()
	runFile := filepath.Join(filepath.Dir(w.input), "running.json")
	path := filepath.Join(filepath.Dir(runFile), "windows-pty-hosts.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry["sessionId"] == w.handle.ID {
			delete(entry, "supervised")
			if entry["launchId"] != w.ref.Generation || entry["pipePath"] == "" {
				t.Fatalf("entry lost its identity: %v", entry)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no registry entry for %s in %s", w.handle.ID, raw)
	}
	rewritten, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), "supervised") {
		t.Fatalf("pre-upgrade layout still names supervision: %s", rewritten)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	return runFile
}

// A session preserved across the upgrade has a registry entry without the
// supervised key. After its agent exits, the adopting runtime must not report
// the still-reporting supervisor as a live agent.
func TestLegacyEntrySupervisorReportingExitIsNotAlive(t *testing.T) {
	w := startSupervisedWindow(t, "legacy-exited", "launch-1", true)
	runFile := rewriteAsPreUpgradeEntry(t, w)
	w.exitAgent(t)
	waitForTestFile(t, w.reporting, "reporting")
	ctx := context.Background()
	adopted := New(Options{RunFilePath: runFile})
	probe := adopted.ProbeFencedRuntime(ctx, w.ref)
	if probe.Liveness == ports.FencedAlive {
		t.Fatalf("adopted pre-upgrade supervisor with an exited agent = %+v, want not alive", probe)
	}
	if probe.Liveness != ports.FencedUnknown || probe.Reason != ports.FencedReasonWorkloadUnconfirmed {
		t.Fatalf("adopted pre-upgrade supervisor = %+v, want unknown/workload_unconfirmed", probe)
	}
	if alive, err := adopted.IsExactSupervisedProcessAlive(ctx, w.handle, w.exact); alive {
		t.Fatalf("exact probe of adopted exited agent = %v, %v; want not alive", alive, err)
	}
	if err := os.WriteFile(w.release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForFencedLiveness(t, adopted, w.ref, ports.FencedDead)
}

// A healthy pre-upgrade supervised session stays deliverable after adoption.
func TestLegacyEntryHealthySupervisorIsAlive(t *testing.T) {
	w := startSupervisedWindow(t, "legacy-healthy", "launch-1", true)
	adopted := New(Options{RunFilePath: rewriteAsPreUpgradeEntry(t, w)})
	ctx := context.Background()
	if probe := adopted.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedAlive {
		t.Fatalf("adopted healthy pre-upgrade supervisor = %+v, want alive", probe)
	}
	if alive, err := adopted.IsExactSupervisedProcessAlive(ctx, w.handle, w.exact); err != nil || !alive {
		t.Fatalf("exact probe = %v, %v; want alive", alive, err)
	}
	w.exitAgent(t)
	waitForTestFile(t, w.reporting, "reporting")
	if probe := adopted.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedDead {
		t.Fatalf("adopted supervisor after its observed agent exited = %+v, want dead", probe)
	}
}

// A pre-upgrade hook-native session runs the agent as the host's direct child.
// It keeps direct-child liveness after adoption.
func TestLegacyEntryDirectChildKeepsDirectLiveness(t *testing.T) {
	w := startSupervisedWindow(t, "legacy-direct", "launch-1", false)
	adopted := New(Options{RunFilePath: rewriteAsPreUpgradeEntry(t, w)})
	ctx := context.Background()
	if probe := adopted.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedAlive {
		t.Fatalf("adopted direct-child agent = %+v, want alive", probe)
	}
	w.exitAgent(t)
	waitForFencedLiveness(t, adopted, w.ref, ports.FencedDead)
}

// When an adopted pre-upgrade host's child cannot be identified, supervision
// stays unknown: never alive from the live child alone, never dead, and not
// cached, so a later readable identity still resolves it.
func TestLegacyEntryUnidentifiableChildIsInconclusive(t *testing.T) {
	w := startSupervisedWindow(t, "legacy-unreadable", "launch-1", true)
	adopted := New(Options{RunFilePath: rewriteAsPreUpgradeEntry(t, w)})
	for _, unreadable := range []func(context.Context, int) ([]string, error){
		func(context.Context, int) ([]string, error) { return nil, errors.New("permission denied") },
		func(context.Context, int) ([]string, error) { return nil, nil },
	} {
		adopted.processArgv = unreadable
		ctx := context.Background()
		if probe := adopted.ProbeFencedRuntime(ctx, w.ref); probe.Liveness != ports.FencedUnknown || probe.Reason != ports.FencedReasonSupervisionUnknown {
			t.Fatalf("unidentifiable adopted host = %+v, want unknown/supervision_unknown", probe)
		}
		if alive, err := adopted.IsExactSupervisedProcessAlive(ctx, w.handle, w.exact); alive || !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
			t.Fatalf("exact probe = %v, %v; want inconclusive", alive, err)
		}
	}
	adopted.processArgv = processArgv
	if probe := adopted.ProbeFencedRuntime(context.Background(), w.ref); probe.Liveness != ports.FencedAlive {
		t.Fatalf("readable identity after failures = %+v, want alive", probe)
	}
}

// An adopted host whose child is a supervisor for another launch is a
// generation mismatch, not this launch's live agent and not its death.
func TestLegacyEntrySupervisorForAnotherLaunchIsMismatch(t *testing.T) {
	w := startSupervisedWindow(t, "legacy-mismatch", "launch-1", true)
	adopted := New(Options{RunFilePath: rewriteAsPreUpgradeEntry(t, w)})
	adopted.processArgv = func(ctx context.Context, pid int) ([]string, error) {
		argv, err := processArgv(ctx, pid)
		for i := range argv {
			if argv[i] == "launch-1" {
				argv[i] = "launch-0"
			}
		}
		return argv, err
	}
	if probe := adopted.ProbeFencedRuntime(context.Background(), w.ref); probe.Liveness != ports.FencedUnknown || probe.Reason != ports.FencedReasonGenerationMismatch {
		t.Fatalf("supervisor for another launch = %+v, want unknown/generation_mismatch", probe)
	}
}
