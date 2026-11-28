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
	// startedAt is each process's wall-clock start, for root start bounds.
	startedAt map[int]time.Time
	killed    []int
	frozen    []int
}

func newScriptedTable(lines ...string) *scriptedTable {
	t := &scriptedTable{processes: map[int]procmem.Process{}, started: map[int]string{}, startedAt: map[int]time.Time{}}
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

// identities is one consistent reading of the table: each pid's parent and
// start identity together.
func (t *scriptedTable) identities(context.Context) (map[int]procmem.Identity, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[int]procmem.Identity{}
	for pid, p := range t.processes {
		out[pid] = procmem.Identity{PPID: p.PPID, Start: t.started[pid], StartedAt: t.startedAt[pid]}
	}
	return out, nil
}

func (t *scriptedTable) freeze(pid int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.frozen = append(t.frozen, pid)
	return nil
}

func (t *scriptedTable) kill(pid int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.killed = append(t.killed, pid)
	delete(t.processes, pid)
	return nil
}

func forceStopLauncher(rt reviewerRuntime, table *scriptedTable) *agentLauncher {
	return &agentLauncher{runtime: rt, forceStop: forceStopHooks{identities: table.identities, kill: table.kill, wait: 50 * time.Millisecond, poll: 5 * time.Millisecond}}
}

// A pid reused by an unrelated process after the agent exited is never killed.
func TestForceStopLeavesAReusedPidAlone(t *testing.T) {
	table := newScriptedTable(
		"44487 1 100 0:00 ao pty-host",
		"44493 44487 100 0:00 claude --review",
	)
	rt := &rootedRuntime{fakeRuntime: &fakeRuntime{}, roots: []int{44487}}
	l := forceStopLauncher(rt, table)
	reads := 0
	l.forceStop.identities = func(ctx context.Context) (map[int]procmem.Identity, error) {
		reads++
		if reads == 2 {
			// After the capture the agent exits and another process takes its
			// pid: same pid and even the same command, but a new start.
			table.mu.Lock()
			table.processes[44493] = procmem.Process{PID: 44493, PPID: 1, Command: "claude --review"}
			table.started[44493] = "later"
			table.mu.Unlock()
		}
		return table.identities(ctx)
	}
	stopped, err := l.ForceStop(context.Background(), "review-mer-1")
	if err != nil || !stopped {
		t.Fatalf("ForceStop = %v, %v; want confirmed", stopped, err)
	}
	for _, pid := range table.killed {
		if pid == 44493 {
			t.Fatalf("killed the process that reused pid 44493: killed=%v", table.killed)
		}
	}
}

// boundedRuntime reports its root with the latest moment that root can have
// started, as the ConPTY runtime does from its spawn and registration times.
type boundedRuntime struct {
	*fakeRuntime
	roots []ports.ProcessRoot
}

func (r *boundedRuntime) BoundedProcessRoots(context.Context, ports.RuntimeHandle) ([]ports.ProcessRoot, error) {
	return r.roots, nil
}

// A runtime can still report a root pid after its host exited, and another
// process may hold that pid now. That process started after the host's start
// bound, so neither it nor anything below it is the reviewer's: nothing is
// killed.
func TestForceStopNeverWalksDownFromAReusedRootPid(t *testing.T) {
	table := newScriptedTable(
		"44487 1 100 0:00 someone-elses-server",
		"44493 44487 100 0:00 someone-elses-worker",
	)
	hostStartedBy := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	table.startedAt[44487] = hostStartedBy.Add(10 * time.Minute)
	table.startedAt[44493] = hostStartedBy.Add(10 * time.Minute)
	rt := &boundedRuntime{fakeRuntime: &fakeRuntime{}, roots: []ports.ProcessRoot{{PID: 44487, StartedBy: hostStartedBy}}}
	stopped, err := forceStopLauncher(rt, table).ForceStop(context.Background(), "review-mer-1")
	if err != nil || !stopped {
		t.Fatalf("ForceStop = %v, %v; want confirmed with nothing of the reviewer's left", stopped, err)
	}
	if len(table.killed) != 0 {
		t.Fatalf("killed unrelated processes %v that took the dead host's pid", table.killed)
	}
}

// A survivor kept from an earlier attempt can exit and have its pid reused
// before the retry. The replacement and the children it starts are not the
// reviewer's: none is frozen or killed. A child the original left behind
// (Windows keeps the dead parent's pid on it) is still killed.
func TestForceStopRetryLeavesAReusedSurvivorPidAndItsChildrenAlone(t *testing.T) {
	table := newScriptedTable(
		"500 1 100 0:00 someone-elses-server",
		"501 500 100 0:00 someone-elses-worker",
		"502 500 100 0:00 claude-tool",
	)
	original := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	table.startedAt[500] = original.Add(100 * time.Second)
	table.startedAt[501] = original.Add(101 * time.Second)
	table.startedAt[502] = original.Add(50 * time.Second)
	l := forceStopLauncher(&rootedRuntime{fakeRuntime: &fakeRuntime{}}, table)
	l.forceStop.freeze = table.freeze
	l.stops.keep("review-mer-1", []capturedProcess{{pid: 500, start: "reviewer", startedAt: original}})

	stopped, err := l.ForceStop(context.Background(), "review-mer-1")
	if err != nil || !stopped {
		t.Fatalf("ForceStop = %v, %v; want confirmed", stopped, err)
	}
	for _, pid := range append(append([]int{}, table.frozen...), table.killed...) {
		if pid == 500 || pid == 501 {
			t.Fatalf("signalled unrelated process %d: frozen=%v killed=%v", pid, table.frozen, table.killed)
		}
	}
	if len(table.killed) != 1 || table.killed[0] != 502 {
		t.Fatalf("killed = %v, want only the reviewer's orphan 502", table.killed)
	}
}
