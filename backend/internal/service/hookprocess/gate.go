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

type Applier interface {
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error
}

type SessionReader interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

const (
	maxRejected = 1024
	rejectedFor = 30 * time.Second
)

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

func (g *Gate) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	if s.ProcessID <= 1 || s.ControllerGeneration != "" || g.sessions == nil || g.runtime == nil {
		return g.next.ApplyActivitySignal(ctx, id, s)
	}
	rec, ok, err := g.sessions.GetSession(ctx, id)
	if err != nil || !ok || rec.Harness != domain.HarnessClaudeCode ||
		domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI ||
		rec.Metadata.RuntimeHandleID == "" || s.LaunchID != rec.Metadata.RuntimeLaunchID {
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
	case terminalRunning && underRoots && descends(parents, terminal.pid, pid):
		g.pin(rec.ID, launchID, pid)
		return true
	case terminalRunning:
	case underRoots:
		g.pin(rec.ID, launchID, pid)
		return true
	default:
		return true
	}
	if len(g.rejected) >= maxRejected {
		g.rejected = make(map[processKey]time.Time)
	}
	g.rejected[key] = g.now()
	return false
}

func (g *Gate) pin(id domain.SessionID, launchID string, pid int) {
	g.terminals[id] = terminalProcess{launchID: launchID, pid: pid}
	for key := range g.rejected {
		if key.session == id {
			delete(g.rejected, key)
		}
	}
}

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
