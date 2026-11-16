package review

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// rootedRuntime is fakeRuntime plus the process-root capability the force
// stop needs to capture the reviewer's tree.
type rootedRuntime struct {
	*fakeRuntime
	roots   []int
	rootErr error
}

func (r *rootedRuntime) ProcessRootPIDs(context.Context, ports.RuntimeHandle) ([]int, error) {
	return r.roots, r.rootErr
}

// scriptedTable is a process table that changes as processes are killed.
type scriptedTable struct {
	mu        sync.Mutex
	processes map[int]procmem.Process
	// started is each process's start time; it survives exec and differs
	// for a reused pid.
	started map[int]string
	killed  []int
}

func newScriptedTable(lines ...string) *scriptedTable {
	t := &scriptedTable{processes: map[int]procmem.Process{}, started: map[int]string{}}
	table, err := procmem.Parse(strings.Join(lines, "\n"))
	if err != nil {
		panic(err)
	}
	for _, p := range table.All() {
		t.processes[p.PID] = p
		t.started[p.PID] = "boot+" + strconv.Itoa(p.PID)
	}
	return t
}

func (t *scriptedTable) starts(context.Context) (map[int]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[int]string{}
	for pid := range t.processes {
		out[pid] = t.started[pid]
	}
	return out, nil
}

func (t *scriptedTable) snapshot(context.Context) (*procmem.Table, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	for _, p := range t.processes {
		lines = append(lines, formatPS(p))
	}
	return procmem.Parse(strings.Join(lines, "\n"))
}

func formatPS(p procmem.Process) string {
	return strings.Join([]string{strconv.Itoa(p.PID), strconv.Itoa(p.PPID), "100", "0:00", p.Command}, " ")
}

func (t *scriptedTable) kill(pid int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.killed = append(t.killed, pid)
	delete(t.processes, pid)
	return nil
}

// destroyWrappers models the runtime's Destroy: the PTY host and AO's
// supervisor exit, and the agent below them is reparented to init.
func (t *scriptedTable) destroyWrappers(pids ...int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, pid := range pids {
		delete(t.processes, pid)
	}
	for pid, p := range t.processes {
		if _, ok := t.processes[p.PPID]; !ok && p.PPID != 1 {
			p.PPID = 1
			t.processes[pid] = p
		}
	}
}

type destroyHookRuntime struct {
	*rootedRuntime
	onDestroy func()
}

func (r *destroyHookRuntime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	if r.onDestroy != nil {
		r.onDestroy()
	}
	return r.rootedRuntime.Destroy(ctx, handle)
}

func forceStopLauncher(rt reviewerRuntime, table *scriptedTable) *agentLauncher {
	return &agentLauncher{runtime: rt, forceStop: forceStopHooks{snapshot: table.snapshot, starts: table.starts, kill: table.kill, wait: 50 * time.Millisecond, poll: 5 * time.Millisecond}}
}

// A pid reused by an unrelated process after the agent exited is never killed.
func TestForceStopLeavesAReusedPidAlone(t *testing.T) {
	table := newScriptedTable(
		"44487 1 100 0:00 ao pty-host",
		"44493 44487 100 0:00 claude --review",
	)
	rt := &destroyHookRuntime{rootedRuntime: &rootedRuntime{fakeRuntime: &fakeRuntime{}, roots: []int{44487}}}
	rt.onDestroy = func() {
		table.destroyWrappers(44487)
		table.mu.Lock()
		table.processes[44493] = procmem.Process{PID: 44493, PPID: 1, Command: "claude --review"}
		table.started[44493] = "later" // same pid and even the same command: another process
		table.mu.Unlock()
	}
	stopped, err := forceStopLauncher(rt, table).ForceStop(context.Background(), "review-mer-1")
	if err != nil || !stopped || len(table.killed) != 0 {
		t.Fatalf("ForceStop = %v, %v killed=%v; want confirmed without killing the reused pid", stopped, err, table.killed)
	}
}
