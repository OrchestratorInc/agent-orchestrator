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
	// immortal pids ignore kills, like a process the OS refuses to end.
	immortal map[int]bool
	killed   []int
}

func newScriptedTable(lines ...string) *scriptedTable {
	t := &scriptedTable{processes: map[int]procmem.Process{}, started: map[int]string{}, immortal: map[int]bool{}}
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
	return &agentLauncher{runtime: rt, forceStop: forceStopHooks{snapshot: table.snapshot, starts: table.starts, kill: table.kill, wait: 50 * time.Millisecond, poll: 5 * time.Millisecond}}
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
		table.processes[44493] = procmem.Process{PID: 44493, PPID: 1, Command: "claude --review"}
		table.started[44493] = "later" // same pid and even the same command: another process
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

// A process that execs during teardown changes its command line but not its
// start time: it is still the reviewer's and must be killed.
func TestForceStopKillsADescendantThatExecdDuringTeardown(t *testing.T) {
	table := newScriptedTable(
		"44487 1 100 0:00 ao pty-host",
		"44493 44487 100 0:00 sh -c exec sleep 600",
	)
	rt := &destroyHookRuntime{rootedRuntime: &rootedRuntime{fakeRuntime: &fakeRuntime{}, roots: []int{44487}}}
	rt.onDestroy = func() {
		table.destroyWrappers(44487)
		table.mu.Lock()
		p := table.processes[44493]
		p.Command = "sleep 600"
		table.processes[44493] = p
		table.mu.Unlock()
	}
	stopped, err := forceStopLauncher(rt, table).ForceStop(context.Background(), "review-mer-1")
	if err != nil || !stopped {
		t.Fatalf("ForceStop = %v, %v; want confirmed after killing the exec'd process", stopped, err)
	}
	if len(table.killed) != 1 || table.killed[0] != 44493 {
		t.Fatalf("killed = %v, want the exec'd descendant 44493", table.killed)
	}
}

// A retry after an unconfirmed stop still knows what the first attempt
// captured: the runtime is gone and reports no roots, but the survivor is
// still the reviewer's and the stop is not confirmed while it lives.
func TestForceStopRetryKeepsTheSurvivorsOfAnEarlierAttempt(t *testing.T) {
	table := newScriptedTable(
		"44487 1 100 0:00 ao pty-host",
		"44493 44487 100 0:00 claude --review",
	)
	table.immortal[44493] = true
	roots := &rootedRuntime{fakeRuntime: &fakeRuntime{}, roots: []int{44487}}
	rt := &destroyHookRuntime{rootedRuntime: roots}
	rt.onDestroy = func() {
		table.destroyWrappers(44487)
		roots.roots = nil // the host is unregistered
	}
	l := forceStopLauncher(rt, table)
	if stopped, _ := l.ForceStop(context.Background(), "review-mer-1"); stopped {
		t.Fatal("first attempt confirmed with the survivor alive")
	}
	if stopped, err := l.ForceStop(context.Background(), "review-mer-1"); stopped || err == nil {
		t.Fatalf("retry falsely confirmed while the survivor lives: %v, %v", stopped, err)
	}
	// Once it can be killed, the retry kills it and confirms.
	table.immortal[44493] = false
	if stopped, err := l.ForceStop(context.Background(), "review-mer-1"); !stopped || err != nil {
		t.Fatalf("retry after the survivor became killable = %v, %v; want confirmed", stopped, err)
	}
	if _, alive := table.processes[44493]; alive {
		t.Fatal("survivor still alive after a confirmed retry")
	}
}
