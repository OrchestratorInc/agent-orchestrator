//go:build !windows

package review

import (
	"context"
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
	return syscall.Kill(r.root, syscall.SIGKILL)
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
	t.Cleanup(func() {
		_ = syscall.Kill(-wrapper.Process.Pid, syscall.SIGKILL)
		_ = wrapper.Wait()
	})
	go func() { _ = wrapper.Wait() }()

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
