//go:build !windows

package review

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// wrapperRuntime is a runtime whose Destroy ends only its root process, as the
// PTY host and AO supervisor did in the reproduced macOS failure.
type wrapperRuntime struct {
	*fakeRuntime
	root int
}

func (r *wrapperRuntime) ProcessRootPIDs(context.Context, ports.RuntimeHandle) ([]int, error) {
	return []int{r.root}, nil
}

func (r *wrapperRuntime) Destroy(context.Context, ports.RuntimeHandle) error {
	// Like the real runtimes, a root that is already gone is torn down.
	if err := syscall.Kill(r.root, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}

// Real processes, real ps, real signals: a stopped agent below a wrapper
// survives the wrapper's teardown, and ForceStop must kill it before it
// confirms the stop.
func TestForceStopKillsAStoppedDescendantOfARealProcessTree(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("ps is not available")
	}
	// The agent ignores hangup and terminate, as the surviving Claude process
	// did: otherwise the kernel's SIGHUP to the orphaned, stopped process
	// group would end it and hide the bug.
	wrapper := exec.Command("sh", "-c", `trap "" HUP TERM; sleep 600 & wait`)
	wrapper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := wrapper.Start(); err != nil {
		t.Fatalf("start wrapper: %v", err)
	}
	// One waiter reaps the wrapper as soon as ForceStop's teardown kills it,
	// so it does not linger as a zombie; cleanup waits on that same waiter.
	waited := make(chan struct{})
	go func() {
		_ = wrapper.Wait()
		close(waited)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-wrapper.Process.Pid, syscall.SIGKILL)
		<-waited
	})

	agent := waitForChild(t, wrapper.Process.Pid)
	if err := syscall.Kill(agent, syscall.SIGSTOP); err != nil {
		t.Fatalf("stop the agent: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(agent, syscall.SIGKILL) })

	l := &agentLauncher{runtime: &wrapperRuntime{fakeRuntime: &fakeRuntime{}, root: wrapper.Process.Pid}}
	stopped, err := l.ForceStop(context.Background(), "review-mer-1")
	if err != nil || !stopped {
		t.Fatalf("ForceStop = %v, %v; want the stopped agent killed and the stop confirmed", stopped, err)
	}
	if processRunning(t, agent) {
		t.Fatalf("stopped agent %d survived a confirmed force-stop", agent)
	}
}

// waitForChild returns the pid of parent's first child once ps shows it.
func waitForChild(t *testing.T, parent int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		table, err := procmem.Snapshot(context.Background(), nil)
		if err != nil {
			t.Fatalf("ps: %v", err)
		}
		for _, p := range table.All() {
			if p.PPID == parent {
				return p.PID
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no child of %d appeared", parent)
	return 0
}

// processRunning reports whether pid is a live (non-zombie) process.
func processRunning(t *testing.T, pid int) bool {
	t.Helper()
	table, err := procmem.Snapshot(context.Background(), nil)
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	for _, p := range table.All() {
		if p.PID == pid {
			return true
		}
	}
	return false
}

// A reviewer that spawns a tool in its own session after the capture, while
// the force-stop is underway, must not leave it running (#4850): once the
// reviewer dies the tool is reparented and its ancestry is lost, so no later
// rescan can find it. The reviewer is released to spawn right after the first
// reading that follows the capture: without the freeze that is the kill
// round's reading, which the tool then escapes; with it the reviewer is
// already stopped and cannot spawn at all.
func TestForceStopCatchesAToolSpawnedDuringTeardown(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is not available")
	}
	gate := t.TempDir() + "/gate"
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	// The reviewer waits on the gate, then starts a detached tool and keeps
	// working; it ignores hangup and terminate, as an agent can.
	const toolCommand = "sleep 6007"
	reviewer := exec.Command("sh", "-c", `trap "" HUP TERM; read _ < "$0"; setsid `+toolCommand+` & wait`, gate)
	reviewer.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := reviewer.Start(); err != nil {
		t.Fatalf("start reviewer: %v", err)
	}
	waited := make(chan struct{})
	go func() {
		_ = reviewer.Wait()
		close(waited)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-reviewer.Process.Pid, syscall.SIGKILL)
		if pid := findProcess(t, toolCommand); pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		<-waited
	})
	// Opening the write end returns once the reviewer is blocked reading it.
	gateFile, err := os.OpenFile(gate, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open gate: %v", err)
	}
	defer gateFile.Close()

	l := &agentLauncher{runtime: &wrapperRuntime{fakeRuntime: &fakeRuntime{}, root: reviewer.Process.Pid}}
	l.forceStop = defaultForceStopHooks()
	reads := 0
	l.forceStop.identities = func(ctx context.Context) (map[int]procmem.Identity, error) {
		ids, err := procmem.Identities(ctx)
		reads++
		if reads == 2 {
			_, _ = gateFile.WriteString("go\n")
			// A running reviewer spawns the tool now; a frozen one cannot.
			deadline := time.Now().Add(time.Second)
			for findProcess(t, toolCommand) == 0 && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
		}
		return ids, err
	}
	stopped, err := l.ForceStop(context.Background(), "review-mer-1")
	if err != nil || !stopped {
		t.Fatalf("ForceStop = %v, %v; want the stop confirmed", stopped, err)
	}
	if pid := findProcess(t, toolCommand); pid > 0 {
		t.Fatalf("tool %d spawned during teardown survived a confirmed force-stop", pid)
	}
}

// findProcess returns the pid of a live process running command, or 0.
func findProcess(t *testing.T, command string) int {
	t.Helper()
	table, err := procmem.Snapshot(context.Background(), nil)
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	for _, p := range table.All() {
		if p.Command == command {
			return p.PID
		}
	}
	return 0
}
