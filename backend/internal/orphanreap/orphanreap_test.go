package orphanreap

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// boot is the reference daemon start: victims must predate it.
var boot = time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)

// snapshotAt is when the process table was read; ages are relative to it.
var snapshotAt = boot.Add(5 * time.Second)

func testDeps(terminated map[string]struct{}) Deps {
	return Deps{
		Boot:           boot,
		WorktreesDir:   "/Users/u/.ao/data/worktrees",
		ExecutableBase: "ao",
		Terminated:     terminated,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// orphanFixture is the three-process chain from the field report: the daemon
// died (PPID=1 on the detached pty-host) and the whole tree kept running. The
// executable path deliberately contains spaces (macOS app bundle) so the
// verb-relative parser is regression-tested on every fixture.
const bundleExe = "/Applications/Agent Orchestrator.app/Contents/Resources/ao"

func orphanTable(t *testing.T) *Table {
	t.Helper()
	out := strings.Join([]string{
		"  100     1  7168  2-00:13:00 " + bundleExe + " pty-host nokia-1 /Users/u/.ao/data/worktrees/proj/nokia-1 bash -lc claude --append-system-prompt-file /tmp/long/prompt.md",
		"  101   100  6144  2-00:12:59 " + bundleExe + " agent-process supervise --session nokia-1 --launch L1 -- claude --append-system-prompt-file /tmp/long/prompt.md",
		"  102   101 153600 2-00:12:58 claude --append-system-prompt-file /tmp/long/prompt.md --resume",
	}, "\n")
	tbl, err := ParseTable(out, snapshotAt)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	return tbl
}

func TestPlanKillsOrphanWithDeadParent(t *testing.T) {
	tbl := orphanTable(t)
	victims := Plan(tbl, snapshotAt, testDeps(map[string]struct{}{"nokia-1": {}}))
	if len(victims) != 1 {
		t.Fatalf("victims = %d (%+v), want exactly the orphaned pty-host tree", len(victims), victims)
	}
	v := victims[0]
	if v.PID != 100 || v.SessionID != "nokia-1" {
		t.Fatalf("victim = pid %d session %q, want pid 100 session nokia-1", v.PID, v.SessionID)
	}
	if got, want := v.Tree, []int{100, 101, 102}; !sameInts(got, want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
}

func TestPlanSparesLiveSessionOfOtherInstallation(t *testing.T) {
	// The id matches a terminated row in THIS instance's database, but the
	// process was spawned by another installation: its workspace sits under a
	// different AO_DATA_DIR's worktrees. Its cwd is the only installation
	// boundary visible in the process table, so it must fence the victim.
	out := "  200     1  7168  2-00:13:00 " + bundleExe + " pty-host nokia-1 /tmp/other-data/worktrees/proj/nokia-1 bash -lc claude"
	tbl, err := ParseTable(out, snapshotAt)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if victims := Plan(tbl, snapshotAt, testDeps(map[string]struct{}{"nokia-1": {}})); len(victims) != 0 {
		t.Fatalf("another installation's process reaped: %+v", victims)
	}

	// Same boundary on the second angle: the cwd is ours, but THIS database
	// says the session is live (adoption owns it, not the reap pass).
	out = "  201     1  7168  2-00:13:00 " + bundleExe + " pty-host nokia-1 /Users/u/.ao/data/worktrees/proj/nokia-1 bash -lc claude"
	tbl, err = ParseTable(out, snapshotAt)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if victims := Plan(tbl, snapshotAt, testDeps(map[string]struct{}{})); len(victims) != 0 {
		t.Fatalf("live session's runtime reaped: %+v", victims)
	}
}

// TestPlanReapsSupersededLiveSessionTree pins the fork issue #115 class: a
// live session's pty-host orphaned by a daemon restart (PPID=1, no registry
// entry) is invisible to Destroy and untouchable while the session is live.
// Once THIS daemon binds a freshly created runtime to the same session id —
// Superseded — the pre-boot tree is provably stale: the session's live runtime
// is the new host, and adoption no longer owns the old one.
func TestPlanReapsSupersededLiveSessionTree(t *testing.T) {
	tbl := orphanTable(t)
	deps := testDeps(map[string]struct{}{})
	deps.Superseded = map[string]struct{}{"nokia-1": {}}
	victims := Plan(tbl, snapshotAt, deps)
	if len(victims) != 1 {
		t.Fatalf("victims = %d (%+v), want exactly the superseded tree", len(victims), victims)
	}
	if victims[0].PID != 100 || victims[0].SessionID != "nokia-1" || !victims[0].Superseded {
		t.Fatalf("victim = pid %d session %q superseded %v, want pid 100 nokia-1 superseded",
			victims[0].PID, victims[0].SessionID, victims[0].Superseded)
	}
	// A tree of another live session that this daemon has not superseded stays
	// untouchable: supersession is per-session evidence, not a blanket pass.
	deps.Superseded = map[string]struct{}{"other-1": {}}
	if victims := Plan(tbl, snapshotAt, deps); len(victims) != 0 {
		t.Fatalf("unrelated live session's runtime reaped: %+v", victims)
	}
}

// A superseded session's tree that started after this daemon booted is a
// runtime of THIS incarnation (or a same-id spawn race) — never a leftover.
func TestPlanSparesSupersededTreeStartedAfterBoot(t *testing.T) {
	out := "  210     1  7168  00:00:02 " + bundleExe + " pty-host nokia-1 /Users/u/.ao/data/worktrees/proj/nokia-1 bash -lc claude"
	tbl, err := ParseTable(out, snapshotAt)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	deps := testDeps(map[string]struct{}{})
	deps.Superseded = map[string]struct{}{"nokia-1": {}}
	if victims := Plan(tbl, snapshotAt, deps); len(victims) != 0 {
		t.Fatalf("this daemon's own runtime reaped: %+v", victims)
	}
}

func TestPlanSparesForeignProcesses(t *testing.T) {
	deps := testDeps(map[string]struct{}{"nokia-1": {}})
	// name bait: a foreign claude process is never a candidate, whatever its age.
	rows := []string{
		// A harness process nobody owns.
		"  300     1 153600 30-00:00:00 claude --resume abc123",
		// "pty-host" appears mid-argv, not as our verb: the executable in front
		// of the verb token is not this daemon's binary.
		"  301     1  1024 30-00:00:00 bash -c pty-host nokia-1 /Users/u/.ao/data/worktrees/proj/nokia-1 sleep 1",
		// Same executable basename family is not enough: a different binary
		// ("ao-clone") at a foreign path is not this daemon's harness.
		"  302     1  7168 30-00:00:00 /opt/foreign/bin/ao-clone pty-host nokia-1 /Users/u/.ao/data/worktrees/proj/nokia-1 bash",
		// Wrong workspace kind: the cwd argument points outside the worktrees
		// prefix (a user-registered repo workspace), so ownership is unproven.
		"  303     1  7168 30-00:00:00 " + bundleExe + " pty-host nokia-1 /Users/u/Projects/proj bash",
	}
	tbl, err := ParseTable(strings.Join(rows, "\n"), snapshotAt)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if victims := Plan(tbl, snapshotAt, deps); len(victims) != 0 {
		t.Fatalf("foreign processes reaped: %+v", victims)
	}
}

func TestPlanSparesProcessStartedAfterBoot(t *testing.T) {
	// Started after this daemon booted: it cannot belong to a past incarnation,
	// so it is only reported. The next boot owns the decision.
	out := "  400     1  7168  0:00:02 " + bundleExe + " pty-host nokia-1 /Users/u/.ao/data/worktrees/proj/nokia-1 bash"
	tbl, err := ParseTable(out, snapshotAt)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if victims := Plan(tbl, snapshotAt, testDeps(map[string]struct{}{"nokia-1": {}})); len(victims) != 0 {
		t.Fatalf("process younger than the daemon reaped: %+v", victims)
	}
}

func TestParseTable(t *testing.T) {
	out := strings.Join([]string{
		"  100     1  7168  2-00:13:00 " + bundleExe + " pty-host nokia-1 /Users/u/.ao/data/worktrees/proj/nokia-1 bash -lc claude",
		"  101   100  6144     11:53 ao agent-process supervise --session nokia-1",
		"  102   101 153600 04:05:06 claude --resume",
		"garbage row",
		"  abc   1  12 0:01 short-circuit-non-numeric-pid",
	}, "\n")
	tbl, err := ParseTable(out, snapshotAt)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	procs := tbl.All()
	if len(procs) != 3 {
		t.Fatalf("parsed %d processes, want 3 valid rows", len(procs))
	}
	first := tbl.ByPID(100)
	if first == nil {
		t.Fatal("pid 100 missing")
	}
	// 2-00:13:00 = 2 days 13 minutes.
	if want := 2*24*time.Hour + 13*time.Minute; first.Age != want {
		t.Fatalf("pid 100 age = %v, want %v", first.Age, want)
	}
	if got := tbl.ByPID(101).Age; got != 11*time.Minute+53*time.Second {
		t.Fatalf("pid 101 age = %v, want 11m53s", got)
	}
	if got := tbl.ByPID(102).Age; got != 4*time.Hour+5*time.Minute+6*time.Second {
		t.Fatalf("pid 102 age = %v, want 4h5m6s", got)
	}
	// args are kept verbatim (bundle path with a space intact).
	if !strings.Contains(first.Args, "Agent Orchestrator.app") {
		t.Fatalf("args lost the spaced path: %q", first.Args)
	}
}

func TestReapSignalsRootThenForcesTree(t *testing.T) {
	tbl := orphanTable(t)
	alive := map[int]bool{100: true, 101: true, 102: true}
	var signaled, killed []int
	k := Killer{
		Grace: 10 * time.Millisecond,
		Poll:  time.Millisecond,
		Signal: func(pid int) error {
			signaled = append(signaled, pid)
			// The graceful path takes the tree root down, but the supervise
			// grandchild survives it in this scenario.
			delete(alive, pid)
			return nil
		},
		Kill: func(pid int) error {
			killed = append(killed, pid)
			delete(alive, pid)
			return nil
		},
		Alive: func(pid int) bool { return alive[pid] },
	}
	victims := Reap(context.Background(), tbl, snapshotAt, testDeps(map[string]struct{}{"nokia-1": {}}), k)
	if len(victims) != 1 || victims[0].PID != 100 {
		t.Fatalf("victims = %+v, want the pid-100 tree", victims)
	}
	if len(signaled) != 1 || signaled[0] != 100 {
		t.Fatalf("signals = %v, want exactly the tree root 100", signaled)
	}
	if !sameInts(killed, []int{101, 102}) {
		t.Fatalf("forced kills = %v, want the surviving tree members [101 102]", killed)
	}
}

func sameInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[int]int, len(want))
	for _, w := range want {
		seen[w]++
	}
	for _, g := range got {
		seen[g]--
		if seen[g] < 0 {
			return false
		}
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
