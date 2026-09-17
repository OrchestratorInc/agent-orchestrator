package usage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

type memorySessionStore interface {
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	ListAllSessions(context.Context) ([]domain.SessionRecord, error)
}

// MemoryReaderDeps wires the memory reader. Snapshot defaults to a real `ps`
// run; tests inject a parsed table.
type MemoryReaderDeps struct {
	Store   memorySessionStore
	Runtime ports.RuntimeProcessRootInspector
	// ChatHostPID names the provider host of a runtime-less Chat session.
	// Nil means Chat sessions are not measured.
	ChatHostPID func(sessionID domain.SessionID) (int, bool)
	Snapshot    func(context.Context) (*procmem.Table, error)
	Now         func() time.Time
	// CacheTTL bounds how often the process table is re-read while several
	// clients poll. Zero disables caching.
	CacheTTL time.Duration
}

// MemoryReader samples resident memory for every live session's runtime.
type MemoryReader struct {
	deps MemoryReaderDeps

	mu       sync.Mutex
	cached   *procmem.Table
	cachedAt time.Time
}

// NewMemoryReader constructs a memory reader.
func NewMemoryReader(deps MemoryReaderDeps) *MemoryReader {
	if deps.Snapshot == nil {
		deps.Snapshot = func(ctx context.Context) (*procmem.Table, error) { return procmem.Snapshot(ctx, nil) }
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &MemoryReader{deps: deps}
}

// ListMemory returns one reading per live session that has a runtime. Sessions
// that are terminated, have no runtime handle, or whose runtime cannot name a
// root pid are omitted rather than reported as zero.
func (r *MemoryReader) ListMemory(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionMemory, error) {
	if r == nil || r.deps.Store == nil || r.deps.Runtime == nil {
		return nil, fmt.Errorf("session memory reader is unavailable")
	}
	var (
		recs []domain.SessionRecord
		err  error
	)
	if projectID == "" {
		recs, err = r.deps.Store.ListAllSessions(ctx)
	} else {
		recs, err = r.deps.Store.ListSessions(ctx, projectID)
	}
	if err != nil {
		return nil, err
	}
	table, err := r.table(ctx)
	if err != nil {
		return nil, err
	}
	sampledAt := r.deps.Now()
	out := make([]domain.SessionMemory, 0, len(recs))
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		roots := r.rootPIDs(ctx, rec)
		if len(roots) == 0 {
			continue
		}
		tree := table.Tree(roots...)
		if len(tree.Processes) == 0 {
			continue
		}
		procs := make([]domain.SessionMemoryProcess, 0, len(tree.Processes))
		for _, p := range tree.Processes {
			procs = append(procs, domain.SessionMemoryProcess{PID: p.PID, PPID: p.PPID, RSSBytes: p.RSSBytes, Command: p.Command})
		}
		out = append(out, domain.SessionMemory{
			SessionID: rec.ID, RSSBytes: tree.RSSBytes, ProcessCount: len(procs),
			SampledAt: sampledAt, Processes: procs,
		})
	}
	return out, nil
}

// rootPIDs names where a session's process tree starts: the runtime handle
// for terminal sessions, the provider host for Chat sessions. Probe failures
// yield no roots so the session is omitted rather than reported as zero.
func (r *MemoryReader) rootPIDs(ctx context.Context, rec domain.SessionRecord) []int {
	if rec.Mode == domain.SessionModeChat {
		if r.deps.ChatHostPID == nil {
			return nil
		}
		pid, ok := r.deps.ChatHostPID(rec.ID)
		if !ok {
			return nil
		}
		return []int{pid}
	}
	if rec.Metadata.RuntimeHandleID == "" {
		return nil
	}
	roots, err := r.deps.Runtime.ProcessRootPIDs(ctx, ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID})
	if err != nil {
		return nil
	}
	return roots
}

func (r *MemoryReader) table(ctx context.Context) (*procmem.Table, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.deps.Now()
	if r.cached != nil && r.deps.CacheTTL > 0 && now.Sub(r.cachedAt) < r.deps.CacheTTL {
		return r.cached, nil
	}
	table, err := r.deps.Snapshot(ctx)
	if err != nil {
		if errors.Is(err, procmem.ErrUnsupported) {
			return nil, err
		}
		return nil, fmt.Errorf("sample process memory: %w", err)
	}
	r.cached, r.cachedAt = table, now
	return table, nil
}
