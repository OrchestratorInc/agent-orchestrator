// Package hookprocess keeps activity hooks from Claude Code processes other
// than a terminal session's own from changing that session.
package hookprocess

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// Applier applies an activity signal: the lifecycle manager.
type Applier interface {
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error
}

// SessionReader reads the session a hook names.
type SessionReader interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// maxRejected bounds the remembered foreign processes, and rejectedFor how
// long one is remembered before its next hook is placed again (the terminal's
// process may have exited since).
const (
	maxRejected = 1024
	rejectedFor = 30 * time.Second
)

// Gate passes a terminal Claude Code session's activity hooks to the
// lifecycle only when they come from the session's own Claude process.
//
// Every Claude process started in the session's worktree or under its
// terminal inherits AO_SESSION_ID and the runtime launch id, and runs AO's
// workspace hooks: an interactive claude the agent starts from a tool, or one
// started from another pane of the session. Their hooks would replace the
// session's activity, conversation and transcript path with their own. (A
// headless `claude -p` is already told apart by its entrypoint; see
// lifecycle's nonInteractiveNativeEntrypoint.) Claude puts the pid of the
// process running a hook in CLAUDE_PID, and the hook client forwards it.
//
// The terminal's process is the first Claude process under the session's
// runtime whose hook arrives, re-pinned to an ancestor Claude process when
// one reports (a daemon restart can first hear from a nested one), and to a
// new process once the pinned one has exited. While it runs, hooks from any
// other process are dropped. A hook the gate cannot place (no process table,
// no runtime roots, a process outside them with nothing pinned) passes, as
// before the gate.
type Gate struct {
	next     Applier
	sessions SessionReader
	runtime  ports.RuntimeProcessRootInspector
	snapshot func(context.Context) (*procmem.Table, error)
	log      *slog.Logger

	mu        sync.Mutex
	terminals map[domain.SessionID]terminalProcess
	rejected  map[processKey]time.Time
	now       func() time.Time
}

type terminalProcess struct {
	launchID string
	pid      int
}

type processKey struct {
	session  domain.SessionID
	launchID string
	pid      int
}

// New wraps next. A nil snapshot reads the process table with ps.
func New(next Applier, sessions SessionReader, runtime ports.RuntimeProcessRootInspector,
	snapshot func(context.Context) (*procmem.Table, error), log *slog.Logger,
) *Gate {
	if snapshot == nil {
		snapshot = func(ctx context.Context) (*procmem.Table, error) { return procmem.Snapshot(ctx, nil) }
	}
	if log == nil {
		log = slog.Default()
	}
	return &Gate{
		next: next, sessions: sessions, runtime: runtime, snapshot: snapshot, log: log,
		terminals: make(map[domain.SessionID]terminalProcess),
		rejected:  make(map[processKey]time.Time),
		now:       time.Now,
	}
}

// ApplyActivitySignal drops a Claude hook from a process other than the
// session's terminal process and applies everything else.
func (g *Gate) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	if s.ProcessID <= 1 || s.ControllerGeneration != "" || g.sessions == nil || g.runtime == nil {
		return g.next.ApplyActivitySignal(ctx, id, s)
	}
	rec, ok, err := g.sessions.GetSession(ctx, id)
	if err != nil || !ok || rec.Harness != domain.HarnessClaudeCode ||
		domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI ||
		rec.Metadata.RuntimeHandleID == "" || s.LaunchID != rec.Metadata.RuntimeLaunchID {
		// The lifecycle reports a missing session and fences other launches.
		return g.next.ApplyActivitySignal(ctx, id, s)
	}
	if !g.fromTerminal(ctx, rec, s.ProcessID) {
		g.log.Debug("hook process gate: dropped a hook from another Claude process in the session",
			"session", id, "pid", s.ProcessID, "event", s.Event)
		return nil
	}
	return g.next.ApplyActivitySignal(ctx, id, s)
}

func (g *Gate) fromTerminal(ctx context.Context, rec domain.SessionRecord, pid int) bool {
	launchID := rec.Metadata.RuntimeLaunchID
	key := processKey{session: rec.ID, launchID: launchID, pid: pid}
	g.mu.Lock()
	terminal, pinned := g.terminals[rec.ID]
	pinned = pinned && terminal.launchID == launchID
	rejectedAt, rejected := g.rejected[key]
	rejected = rejected && g.now().Sub(rejectedAt) < rejectedFor
	g.mu.Unlock()
	if pinned && terminal.pid == pid {
		return true
	}
	if rejected {
		return false
	}
	table, err := g.snapshot(ctx)
	if err != nil {
		return true
	}
	roots, err := g.runtime.ProcessRootPIDs(ctx, ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID})
	if err != nil || len(roots) == 0 {
		return true
	}
	parents := make(map[int]int)
	for _, process := range table.All() {
		parents[process.PID] = process.PPID
	}
	if _, running := parents[pid]; !running {
		// Gone before the hook arrived: nothing to compare.
		return !pinned
	}
	terminalRunning := pinned
	if pinned {
		_, terminalRunning = parents[terminal.pid]
	}
	underRoots := descends(parents, pid, roots...)

	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case terminalRunning && descends(parents, pid, terminal.pid):
		// A Claude process the terminal's agent started.
	case terminalRunning && underRoots && descends(parents, terminal.pid, pid):
		// The pinned process was one this process started: this is the
		// terminal's.
		g.pin(rec.ID, launchID, pid)
		return true
	case terminalRunning:
		// Another Claude process in the session's panes or worktree.
	case underRoots:
		g.pin(rec.ID, launchID, pid)
		return true
	default:
		// Nothing to place it against: apply, as before the gate.
		return true
	}
	if len(g.rejected) >= maxRejected {
		g.rejected = make(map[processKey]time.Time)
	}
	g.rejected[key] = g.now()
	return false
}

// pin makes pid the session's terminal process. Callers hold g.mu.
func (g *Gate) pin(id domain.SessionID, launchID string, pid int) {
	g.terminals[id] = terminalProcess{launchID: launchID, pid: pid}
	for key := range g.rejected {
		if key.session == id {
			delete(g.rejected, key)
		}
	}
}

// descends reports whether pid is one of ancestors or runs under one.
func descends(parents map[int]int, pid int, ancestors ...int) bool {
	for steps := 0; pid > 1 && steps < 256; steps++ {
		for _, ancestor := range ancestors {
			if pid == ancestor {
				return true
			}
		}
		parent, ok := parents[pid]
		if !ok || parent == pid {
			return false
		}
		pid = parent
	}
	return false
}
