package procinventory

import (
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// sessionIDShape matches domain session ids: domain.RuntimeHandleName keeps
// [a-zA-Z0-9_-] and caps the length at 48 characters.
var sessionIDShape = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,48}$`)

// Tree lifecycle states.
const (
	StateOwned   = "owned"   // session is live in the daemon's DB — never killable
	StateOrphan  = "orphan"  // no live session and no plausible owner — killable
	StateForeign = "foreign" // another live process (e.g. a second AO daemon) owns the tree
)

// Tree is one `ao pty-host` process tree: the detached PTY host, its PTY
// child, the `agent-process supervise` wrapper, and the agent process.
type Tree struct {
	SessionID  string
	RootPID    int
	RootLstart string
	PGID       int
	PIDCount   int
	RSSBytes   int64
	Kind       string // "worker" | "orchestrator" from the DB row; "" when unknown
	State      string
	// Attached reports whether the root's parent is this daemon. An owned tree
	// whose root has PPID 1 was adopted after a daemon restart — owned either
	// way, and never killable.
	Attached bool
	// Activity is the live session's tracked activity state ("active", "idle",
	// "waiting_input", "blocked", "exited") and its last-activity timestamp;
	// both zero for orphan/foreign trees that have no live row.
	ActivityState  string
	LastActivityAt time.Time
}

// Remnant is a supervise/agent chain whose pty-host root (the process-group
// leader) is already dead. Group kill is impossible without the leader, so
// remnants are display-only in this MVP.
type Remnant struct {
	SessionID string
	PID       int
	RSSBytes  int64
}

// GroupSummary is a singleton process group (the daemon, the tmux server).
type GroupSummary struct {
	PID      int
	Present  bool
	RSSBytes int64
}

type Totals struct {
	SessionsCount    int
	SessionsRSSBytes int64
	OrphansCount     int
	OrphansRSSBytes  int64
	ForeignCount     int
	ForeignRSSBytes  int64
	DaemonRSSBytes   int64
	TmuxRSSBytes     int64
}

type Inventory struct {
	GeneratedAt time.Time
	Daemon      GroupSummary
	Tmux        GroupSummary
	Trees       []Tree
	Remnants    []Remnant
	Totals      Totals
	// Host is the host memory snapshot, nil when the platform does not
	// provide one or the fetch failed — the status bar hides its host
	// section on nil.
	Host *HostStats
}

// isPtyHostRoot reports whether the command line is a detached pty-host spawn
// (`<path-to-ao> pty-host <sessionID> <cwd> ...`, built in
// conpty/spawn_unix.go defaultSpawnHost). Token positions are relative to the
// "pty-host" verb, not the front of the line: the packaged binary path
// contains a space ("/Applications/Agent Orchestrator.app/.../ao"), and ps
// renders args unquoted, so the executable path splits into several fields.
// The token before the verb must end in the ao binary name; nothing after the
// session id is read (the cwd can contain spaces too).
func isPtyHostRoot(command string) (domain.SessionID, bool) {
	fields := strings.Fields(command)
	for i := 1; i+1 < len(fields); i++ {
		if fields[i] != "pty-host" {
			continue
		}
		exe := fields[i-1]
		if exe != "ao" && !strings.HasSuffix(exe, "/ao") {
			continue
		}
		if !sessionIDShape.MatchString(fields[i+1]) {
			return "", false
		}
		return domain.SessionID(fields[i+1]), true
	}
	return "", false
}

// superviseSessionID extracts the --session argument from an
// `ao agent-process supervise --session <id> --launch ...` command line.
func superviseSessionID(command string) (domain.SessionID, bool) {
	fields := strings.Fields(command)
	for i := 0; i+3 < len(fields); i++ {
		if fields[i] == "agent-process" && fields[i+1] == "supervise" && fields[i+2] == "--session" {
			if sessionIDShape.MatchString(fields[i+3]) {
				return domain.SessionID(fields[i+3]), true
			}
			return "", false
		}
	}
	return "", false
}

func isTmuxCommand(command string, tmuxSocketName string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 || path.Base(fields[0]) != "tmux" {
		return false
	}
	// With a private socket configured, count only that server — a user's
	// personal tmux server is not AO's footprint.
	if tmuxSocketName == "" {
		return true
	}
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "-L" && fields[i+1] == tmuxSocketName {
			return true
		}
	}
	return false
}

// BuildInventory classifies one process-table snapshot against the daemon's
// live session set. Classification is live-set-first: a tree whose session is
// live in the DB is owned even when launchd adopted its root (PPID 1) after a
// daemon restart — "PPID differs from the daemon" alone would mark every
// surviving session an orphan across restarts and let Kill murder live work.
func BuildInventory(entries []Entry, live map[domain.SessionID]domain.SessionRecord, daemonPID int, tmuxSocketName string, now time.Time) Inventory {
	inv := Inventory{GeneratedAt: now, Trees: []Tree{}, Remnants: []Remnant{}}
	byPID := make(map[int]Entry, len(entries))
	for _, entry := range entries {
		byPID[entry.PID] = entry
		if entry.PID == daemonPID {
			inv.Daemon = GroupSummary{PID: entry.PID, Present: true, RSSBytes: entry.RSSBytes()}
		}
		if isTmuxCommand(entry.Command, tmuxSocketName) {
			// The server out-weighs its momentary clients; keep the largest row.
			if !inv.Tmux.Present || entry.RSSBytes() > inv.Tmux.RSSBytes {
				inv.Tmux = GroupSummary{PID: entry.PID, Present: true, RSSBytes: entry.RSSBytes()}
			}
		}
	}

	rootDescendants := make(map[int]map[int]bool)
	for _, entry := range entries {
		sessionID, ok := isPtyHostRoot(entry.Command)
		if !ok {
			continue
		}
		descendants := Descendants(entries, entry.PID)
		rootDescendants[entry.PID] = descendants
		tree := Tree{
			SessionID:  string(sessionID),
			RootPID:    entry.PID,
			RootLstart: entry.Lstart,
			PGID:       entry.PGID,
			PIDCount:   len(descendants),
		}
		for _, other := range entries {
			if descendants[other.PID] {
				tree.RSSBytes += other.RSSBytes()
			}
		}
		if row, isLive := live[sessionID]; isLive {
			tree.State = StateOwned
			tree.Kind = string(row.Kind)
			tree.Attached = entry.PPID == daemonPID
			tree.ActivityState = string(row.Activity.State)
			tree.LastActivityAt = row.Activity.LastActivityAt
		} else {
			_, parentInSnapshot := byPID[entry.PPID]
			switch {
			case entry.PPID == daemonPID || entry.PPID == 1 || !parentInSnapshot:
				tree.State = StateOrphan
			default:
				tree.State = StateForeign
			}
		}
		inv.Trees = append(inv.Trees, tree)
	}

	// Remnants: supervise chains whose root leader is gone — their session is
	// not live, they descend from no detected tree root, and their own parent
	// is gone (launchd-adopted, PPID 1) or absent from the snapshot.
	for _, entry := range entries {
		sessionID, ok := superviseSessionID(entry.Command)
		if !ok {
			continue
		}
		if _, isLive := live[sessionID]; isLive {
			continue
		}
		if entry.PPID != 1 {
			if _, parentAlive := byPID[entry.PPID]; parentAlive {
				continue
			}
		}
		inTree := false
		for _, descendants := range rootDescendants {
			if descendants[entry.PID] {
				inTree = true
				break
			}
		}
		if inTree {
			continue
		}
		inv.Remnants = append(inv.Remnants, Remnant{SessionID: string(sessionID), PID: entry.PID, RSSBytes: entry.RSSBytes()})
	}

	for _, tree := range inv.Trees {
		switch tree.State {
		case StateOwned:
			inv.Totals.SessionsCount++
			inv.Totals.SessionsRSSBytes += tree.RSSBytes
		case StateOrphan:
			inv.Totals.OrphansCount++
			inv.Totals.OrphansRSSBytes += tree.RSSBytes
		case StateForeign:
			inv.Totals.ForeignCount++
			inv.Totals.ForeignRSSBytes += tree.RSSBytes
		}
	}
	inv.Totals.DaemonRSSBytes = inv.Daemon.RSSBytes
	inv.Totals.TmuxRSSBytes = inv.Tmux.RSSBytes
	return inv
}
