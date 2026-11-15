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
	// immortal pids ignore kills, like a process the OS refuses to end.
	immortal map[int]bool
	killed   []int
}

func newScriptedTable(lines ...string) *scriptedTable {
	t := &scriptedTable{processes: map[int]procmem.Process{}, immortal: map[int]bool{}}
	table, err := procmem.Parse(strings.Join(lines, "\n"))
	if err != nil {
		panic(err)
	}
	for _, p := range table.All() {
		t.processes[p.PID] = p
	}
	return t
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
	if !t.immortal[pid] {
		delete(t.processes, pid)
	}
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
	return &agentLauncher{runtime: rt, forceStop: forceStopHooks{snapshot: table.snapshot, kill: table.kill, wait: 50 * time.Millisecond, poll: 5 * time.Millisecond}}
}

// The reproduced failure: Destroy ends the PTY host (44487) and supervisor
// (44490), but the stopped Claude process (44493) survives, reparented to
// init. ForceStop kills it and only then confirms the stop.
func TestForceStopKillsAnAgentTheRuntimeLeftBehind(t *testing.T) {
	table := newScriptedTable(
		"44487 1 100 0:00 ao pty-host",
		"44490 44487 100 0:00 ao agent-process supervise --session review-mer-1",
		"44493 44490 100 0:00 claude --review",
		"500 1 100 0:00 unrelated",
	)
	rt := &destroyHookRuntime{rootedRuntime: &rootedRuntime{fakeRuntime: &fakeRuntime{}, roots: []int{44487}}}
	rt.onDestroy = func() { table.destroyWrappers(44487, 44490) }
	l := forceStopLauncher(rt, table)

	stopped, err := l.ForceStop(context.Background(), "review-mer-1")
	if err != nil || !stopped {
		t.Fatalf("ForceStop = %v, %v; want confirmed", stopped, err)
	}
	if len(table.killed) != 1 || table.killed[0] != 44493 {
		t.Fatalf("killed = %v, want only the surviving agent 44493", table.killed)
	}
}

func TestForceStopIsUnconfirmedWhileAnyReviewerProcessSurvives(t *testing.T) {
	table := newScriptedTable(
		"44487 1 100 0:00 ao pty-host",
		"44493 44487 100 0:00 claude --review",
	)
	table.immortal[44493] = true
	rt := &destroyHookRuntime{rootedRuntime: &rootedRuntime{fakeRuntime: &fakeRuntime{}, roots: []int{44487}}}
	rt.onDestroy = func() { table.destroyWrappers(44487) }
	stopped, err := forceStopLauncher(rt, table).ForceStop(context.Background(), "review-mer-1")
	if stopped || err == nil || !strings.Contains(err.Error(), "44493") {
		t.Fatalf("ForceStop = %v, %v; want unconfirmed naming 44493", stopped, err)
	}
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
		table.processes[44493] = procmem.Process{PID: 44493, PPID: 1, Command: "someone-else"}
		table.mu.Unlock()
	}
	stopped, err := forceStopLauncher(rt, table).ForceStop(context.Background(), "review-mer-1")
	if err != nil || !stopped || len(table.killed) != 0 {
		t.Fatalf("ForceStop = %v, %v killed=%v; want confirmed without killing the reused pid", stopped, err, table.killed)
	}
}

// Without the reviewer's process tree, wrapper liveness proves nothing, so
// the stop is never confirmed.
func TestForceStopIsUnconfirmedWithoutTheProcessTree(t *testing.T) {
	table := newScriptedTable()
	stopped, err := forceStopLauncher(&fakeRuntime{}, table).ForceStop(context.Background(), "review-mer-1")
	if stopped || err == nil {
		t.Fatalf("ForceStop without process roots = %v, %v; want unconfirmed", stopped, err)
	}
}
