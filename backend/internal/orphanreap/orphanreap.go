// Package orphanreap reaps the detached worker process trees of a dead
// predecessor daemon. When a daemon dies, its pty-host chains
// (pty-host -> agent-process supervise -> claude) reparent to launchd and run
// indefinitely (fork issue #39). At boot — and once more at graceful shutdown —
// this pass kills the trees whose session rows the database already marks
// terminated.
//
// Victim identification is evidence-based, never name- or age-based. A process
// is a victim only when every signal agrees:
//
//   - its argv carries this daemon's executable followed by the "pty-host"
//     verb (parsed verb-relative, so spaces inside the bundle path are safe);
//   - the positional session id after the verb matches a row that is currently
//     is_terminated in this instance's own database;
//   - the positional cwd argument points under this installation's worktrees
//     directory, fencing out other AO installations (each keeps its own
//     AO_DATA_DIR) and user-registered repo workspaces;
//   - the process started before this daemon booted, so it cannot be a spawn
//     of the running incarnation.
//
// Any near-miss (right verb shape, one signal disagreeing) is logged and left
// alone. Foreign processes without the verb shape are not even candidates.
package orphanreap

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// sessionIDPattern mirrors the runtime adapters' session id shape: the
// pty-host argv carries the id positionally, so the pattern is part of the
// identity evidence, not a heuristic.
var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// hostFlags are the optional leading pty-host arguments (conpty spawn.go,
// splitHostOptionArgs): they sit between the verb and the positional
// session id.
var hostFlags = map[string]bool{"--start=attach": true, "--surface=lazy": true}

// Proc is one row of the process table.
type Proc struct {
	PID  int
	PPID int
	// Age is the wall-clock time since the process started (ps etime). A
	// negative value means the start time could not be parsed, which makes the
	// process unprovable and therefore untouchable.
	Age  time.Duration
	Args string
}

// Table is a parsed snapshot of the process table with a parent index.
type Table struct {
	byPID    map[int]Proc
	children map[int][]int
	order    []int
}

// ParseTable parses `ps -axww -o pid=,ppid=,rss=,etime=,args=` output. The rss
// column is read and discarded (it only keeps the column shape aligned with
// the process table's other consumers). Rows that do not match the column
// shape, zombies included, are skipped: absence of evidence for a row can only
// shrink the reap list, never grow it. at is the moment the snapshot was
// taken, so etime ages convert to absolute start evidence.
func ParseTable(out string, at time.Time) (*Table, error) {
	t := &Table{byPID: make(map[int]Proc), children: make(map[int][]int)}
	sc := bufio.NewScanner(strings.NewReader(out))
	// A long args= column (electron/node argv) can exceed the scanner's
	// default 64 KiB token limit; grow it rather than silently truncate.
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t")
		if line == "" {
			continue
		}
		pid, ppid, age, args, ok := splitPSRow(line)
		if !ok || pid <= 0 {
			continue
		}
		// A zombie has exited and holds nothing; listing it reads as a leak.
		if strings.Contains(args, "<defunct>") {
			continue
		}
		t.byPID[pid] = Proc{PID: pid, PPID: ppid, Age: age, Args: args}
		t.children[ppid] = append(t.children[ppid], pid)
		t.order = append(t.order, pid)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("orphanreap: scan ps output: %w", err)
	}
	return t, nil
}

// splitPSRow reads the fixed numeric columns pid=, ppid=, rss= and etime= and
// returns everything after them as the verbatim command line. The verbatim
// tail is what keeps executable paths with spaces intact; column positions are
// counted from the START of the row (ps emits the numeric columns first), and
// the identity fields that vary (executable, verb, session id, cwd) are only
// ever read relative to each other.
func splitPSRow(line string) (pid, ppid int, age time.Duration, args string, ok bool) {
	rest := line
	var nums [3]int // pid, ppid, rss
	for i := range nums {
		rest = strings.TrimLeft(rest, " \t")
		end := strings.IndexAny(rest, " \t")
		if end <= 0 {
			return 0, 0, 0, "", false
		}
		n, err := strconv.Atoi(rest[:end])
		if err != nil {
			return 0, 0, 0, "", false
		}
		nums[i] = n
		rest = rest[end:]
	}
	rest = strings.TrimLeft(rest, " \t")
	end := strings.IndexAny(rest, " \t")
	etime := rest
	if end >= 0 {
		etime = rest[:end]
		rest = rest[end:]
	}
	secs, ok := parseEtime(etime)
	if !ok {
		return 0, 0, 0, "", false
	}
	args = strings.TrimLeft(rest, " \t")
	if args == "" {
		return 0, 0, 0, "", false
	}
	return nums[0], nums[1], time.Duration(secs * float64(time.Second)), args, true
}

// parseEtime reads ps etime ([[dd-]hh:]mm:ss, locale-free on Linux and macOS).
func parseEtime(s string) (float64, bool) {
	days := 0.0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return 0, false
		}
		days, s = float64(n), rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var secs float64
	for _, part := range parts {
		n, err := strconv.ParseFloat(part, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		secs = secs*60 + n
	}
	return days*86400 + secs, true
}

// All returns every parsed process in row order.
func (t *Table) All() []Proc {
	out := make([]Proc, 0, len(t.order))
	for _, pid := range t.order {
		out = append(out, t.byPID[pid])
	}
	return out
}

// ByPID returns one process, or nil when absent.
func (t *Table) ByPID(pid int) *Proc {
	if p, ok := t.byPID[pid]; ok {
		return &p
	}
	return nil
}

// Tree returns the root and every descendant pid, one entry each, in
// discovery order. Only pids present in this snapshot are collected, so the
// kill list can never outlive the evidence.
func (t *Table) Tree(root int) []int {
	out := make([]int, 0, 4)
	seen := make(map[int]bool)
	queue := []int{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		if _, ok := t.byPID[pid]; !ok && pid != root {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
		queue = append(queue, t.children[pid]...)
	}
	return out
}

// Deps carries the ownership evidence Plan checks against. Terminated holds
// the session ids whose database rows are currently is_terminated in this
// instance's own store.
type Deps struct {
	// Boot is this daemon's start time. Processes that started later cannot
	// belong to a past incarnation and are only reported.
	Boot time.Time
	// WorktreesDir is this instance's <dataDir>/worktrees absolute prefix.
	WorktreesDir string
	// ExecutableBase is the basename of this daemon's own executable. The
	// basename, not the full path: a pty-host spawned by a previous, since-
	// updated install carries the old binary's path.
	ExecutableBase string
	Terminated     map[string]struct{}
	// Superseded holds live session ids whose live runtime THIS daemon bound
	// freshly in this incarnation (fork issue #115). A pre-boot pty-host tree
	// carrying such an id can no longer be the session's live runtime: the
	// fresh host is, and the old daemon's host — registry-lost, PPID=1 — is
	// provably stale. Supersession is per-session: ids outside both maps keep
	// the adoption owns it protection.
	Superseded map[string]struct{}
	Logger     *slog.Logger
}

// Victim is one decided orphan: the pty-host root, its session id, and the
// exact pid set the snapshot proves belong to its tree.
type Victim struct {
	SessionID string
	PID       int
	Tree      []int
	Args      string
	// Superseded marks a victim decided by the #115 supersession evidence
	// (live session, fresh runtime bound by this daemon) rather than a
	// terminated row, so the reap log can say which fact condemned the tree.
	Superseded bool
}

// Plan decides which processes are provably orphaned workers of already
// terminated sessions. Near-misses are logged and excluded; processes without
// the pty-host verb shape are not candidates at all and stay silent, so a
// foreign process with the same name can never be reaped by this pass.
func Plan(tbl *Table, at time.Time, deps Deps) []Victim {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	var victims []Victim
	for _, p := range tbl.All() {
		session, superseded, ok := matchOrphan(p, at, deps, logger)
		if !ok {
			continue
		}
		victims = append(victims, Victim{
			SessionID:  session,
			PID:        p.PID,
			Tree:       tbl.Tree(p.PID),
			Args:       p.Args,
			Superseded: superseded,
		})
	}
	return victims
}

// matchOrphan applies the evidence stack to one process. Every disagreement is
// logged with its reason — the report is the deliverable for dubious cases,
// the kill is reserved for full agreement.
func matchOrphan(p Proc, at time.Time, deps Deps, logger *slog.Logger) (string, bool, bool) {
	tokens := strings.Fields(p.Args)
	if len(tokens) < 3 {
		return "", false, false
	}
	verb := -1
	for i := 1; i < len(tokens); i++ {
		if tokens[i] == "pty-host" {
			verb = i
			break
		}
	}
	if verb < 0 {
		return "", false, false
	}
	dubious := func(reason string) (string, bool, bool) {
		logger.Warn("orphanreap: pty-host candidate not reaped",
			"pid", p.PID, "reason", reason, "args", truncate(p.Args))
		return "", false, false
	}
	// Everything before the verb is the executable path; ps preserves the
	// spaces inside it, so the basename is read off the joined prefix. The
	// join is what keeps a spaced bundle path ("Agent Orchestrator.app")
	// parsing as one executable rather than as stray positional tokens.
	if filepath.Base(strings.Join(tokens[:verb], " ")) != deps.ExecutableBase {
		return dubious("executable is not this daemon's binary")
	}
	j := verb + 1
	for j < len(tokens) && hostFlags[tokens[j]] {
		j++
	}
	if len(tokens)-j < 2 {
		return dubious("argv too short for positional <sessionID> <cwd>")
	}
	id := tokens[j]
	if !sessionIDPattern.MatchString(id) {
		return dubious("session id token does not match the AO id shape")
	}
	if !underDir(tokens[j+1], deps.WorktreesDir) {
		return dubious("cwd argument is outside this installation's worktrees")
	}
	_, terminated := deps.Terminated[id]
	_, superseded := deps.Superseded[id]
	if !terminated && !superseded {
		return dubious("session is neither terminated nor superseded by a fresh runtime")
	}
	if p.Age < 0 {
		return dubious("process start time unknown")
	}
	if !at.Add(-p.Age).Before(deps.Boot) {
		return dubious("process started after this daemon booted")
	}
	return id, superseded, true
}

// underDir reports whether path is strictly inside dir. The worktrees root
// itself is not a workspace, so equality is rejected.
func underDir(path, dir string) bool {
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	path = filepath.Clean(path)
	return path != dir && strings.HasPrefix(path, dir+string(filepath.Separator))
}

// Killer is the process-control seam. The defaults signal SIGTERM (the
// pty-host installs a handler that closes its PTY, which ends the agent chain
// beneath it) and fall back to Kill for survivors.
type Killer struct {
	Signal func(pid int) error
	Kill   func(pid int) error
	Alive  func(pid int) bool
	Grace  time.Duration
	Poll   time.Duration
}

// NewKiller returns the production Killer.
func NewKiller() Killer {
	find := func(pid int) (*os.Process, error) {
		p, err := os.FindProcess(pid)
		if err != nil {
			return nil, fmt.Errorf("orphanreap: find pid %d: %w", pid, err)
		}
		return p, nil
	}
	return Killer{
		Signal: func(pid int) error {
			p, err := find(pid)
			if err != nil {
				return err
			}
			return p.Signal(syscall.SIGTERM)
		},
		Kill: func(pid int) error {
			p, err := find(pid)
			if err != nil {
				return err
			}
			return p.Kill()
		},
		Alive: func(pid int) bool {
			p, err := find(pid)
			return err == nil && p.Signal(syscall.Signal(0)) == nil
		},
	}
}

// Reap plans and executes the teardown: SIGTERM to each tree root, a bounded
// grace wait, then SIGKILL for whatever the snapshot proved part of the tree
// and is still alive. It returns the victims it acted on. Every pid comes from
// the snapshot taken before any signal, so the kill list never grows mid-run.
func Reap(ctx context.Context, tbl *Table, at time.Time, deps Deps, k Killer) []Victim {
	victims := Plan(tbl, at, deps)
	if len(victims) == 0 {
		return nil
	}
	if k.Grace <= 0 {
		k.Grace = 3 * time.Second
	}
	if k.Poll <= 0 {
		k.Poll = 100 * time.Millisecond
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	for _, v := range victims {
		if v.Superseded {
			logger.Warn("orphanreap: reaping stale worker tree superseded by this daemon's fresh runtime",
				"session", v.SessionID, "pid", v.PID, "tree", v.Tree, "args", truncate(v.Args))
		} else {
			logger.Warn("orphanreap: reaping orphaned worker tree of a terminated session",
				"session", v.SessionID, "pid", v.PID, "tree", v.Tree, "args", truncate(v.Args))
		}
		if err := k.Signal(v.PID); err != nil {
			logger.Warn("orphanreap: graceful signal failed, forcing", "pid", v.PID, "err", err)
		}
		if !waitGone(ctx, k, v.Tree, k.Grace, k.Poll) && ctx.Err() == nil {
			force(ctx, k, logger, v)
		}
	}
	return victims
}

// force kills every surviving tree member by pid and reports stragglers.
func force(ctx context.Context, k Killer, logger *slog.Logger, v Victim) {
	for _, pid := range v.Tree {
		if !k.Alive(pid) {
			continue
		}
		if err := k.Kill(pid); err != nil {
			logger.Warn("orphanreap: force kill failed", "session", v.SessionID, "pid", pid, "err", err)
		}
	}
	if !waitGone(ctx, k, v.Tree, k.Grace, k.Poll) && ctx.Err() == nil {
		var survivors []int
		for _, pid := range v.Tree {
			if k.Alive(pid) {
				survivors = append(survivors, pid)
			}
		}
		logger.Error("orphanreap: tree members survived teardown",
			"session", v.SessionID, "pids", survivors)
	}
}

// waitGone polls until every pid in the tree is dead or the grace expires.
func waitGone(ctx context.Context, k Killer, tree []int, grace, poll time.Duration) bool {
	deadline := time.Now().Add(grace)
	for {
		if !anyAlive(k, tree) {
			return true
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return false
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func anyAlive(k Killer, tree []int) bool {
	for _, pid := range tree {
		if k.Alive(pid) {
			return true
		}
	}
	return false
}

// maxArgsLen caps logged command lines (procmem's precedent).
const maxArgsLen = 200

func truncate(args string) string {
	if len(args) > maxArgsLen {
		return args[:maxArgsLen]
	}
	return args
}

// ErrSnapshotUnavailable is wrapped by the platform ps runner failures so the
// caller can distinguish "cannot decide" from "decided nothing".
var ErrSnapshotUnavailable = errors.New("orphanreap: process table unavailable")
