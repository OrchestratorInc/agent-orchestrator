package usage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

type memorySessionStore interface {
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	ListAllSessions(context.Context) ([]domain.SessionRecord, error)
}

type memoryReviewerStore interface {
	ListLiveReviewerHandles(context.Context) ([]domain.ReviewerHandle, error)
}

type MemoryReaderDeps struct {
	Store       memorySessionStore
	Runtime     ports.RuntimeProcessRootInspector
	Reviewers   memoryReviewerStore
	ChatHostPID func(sessionID domain.SessionID) (int, bool)
	Snapshot    func(context.Context) (*procmem.Table, error)
	Now         func() time.Time
	AppRootPIDs func() []int
	CacheTTL    time.Duration
}

type MemoryReader struct {
	deps MemoryReaderDeps

	mu             sync.Mutex
	cached         *procmem.Table
	cachedAt       time.Time
	prev           *procmem.Table
	prevAt         time.Time
	lastSystem     procmem.System
	lastSystemAt   time.Time
	cachedSystem   domain.SystemMemory
	cachedSystemAt time.Time
	systemFlight   singleflight.Group
	ReadSystem     func() (procmem.System, error)
	ReadPressure   func() (procmem.Pressure, error)
}

func NewMemoryReader(deps MemoryReaderDeps) *MemoryReader {
	if deps.Snapshot == nil {
		deps.Snapshot = func(ctx context.Context) (*procmem.Table, error) { return procmem.Snapshot(ctx, nil) }
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &MemoryReader{deps: deps, ReadSystem: procmem.ReadSystem, ReadPressure: procmem.ReadPressure}
}

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
	table, prev, elapsed, err := r.table(ctx)
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
		reading := treeReading(tree, prev, elapsed)
		reading.SessionID, reading.SampledAt = rec.ID, sampledAt
		out = append(out, reading)
	}
	return out, nil
}

func treeReading(tree procmem.Tree, prev *procmem.Table, elapsed float64) domain.SessionMemory {
	procs := make([]domain.SessionMemoryProcess, 0, len(tree.Processes))
	for _, p := range tree.Processes {
		procs = append(procs, domain.SessionMemoryProcess{
			PID: p.PID, PPID: p.PPID, RSSBytes: p.RSSBytes, Command: p.Command,
			CPUPercent: procmem.CPUPercent([]procmem.Process{p}, prev, elapsed),
		})
	}
	return domain.SessionMemory{
		RSSBytes: tree.RSSBytes, ProcessCount: len(procs),
		CPUPercent: procmem.CPUPercent(tree.Processes, prev, elapsed), Processes: procs,
	}
}

func (r *MemoryReader) Pressure(context.Context) (procmem.Pressure, error) {
	if r == nil || r.ReadPressure == nil {
		return procmem.Pressure{}, procmem.ErrUnsupported
	}
	return r.ReadPressure()
}

func (r *MemoryReader) SystemMemory(ctx context.Context) (domain.SystemMemory, error) {
	now := r.deps.Now()
	r.mu.Lock()
	if r.deps.CacheTTL > 0 && !r.cachedSystemAt.IsZero() && now.Sub(r.cachedSystemAt) < r.deps.CacheTTL {
		out := r.cachedSystem
		r.mu.Unlock()
		return out, nil
	}
	r.mu.Unlock()
	readCtx := context.WithoutCancel(ctx)
	v, err, _ := r.systemFlight.Do("system", func() (any, error) {
		out, err := r.readSystemMemory(readCtx)
		if err != nil {
			return domain.SystemMemory{}, err
		}
		r.mu.Lock()
		r.cachedSystem, r.cachedSystemAt = out, now
		r.mu.Unlock()
		return out, nil
	})
	if err != nil {
		return domain.SystemMemory{}, err
	}
	out, ok := v.(domain.SystemMemory)
	if !ok {
		return domain.SystemMemory{}, fmt.Errorf("system memory flight returned %T", v)
	}
	return out, nil
}

func (r *MemoryReader) readSystemMemory(ctx context.Context) (domain.SystemMemory, error) {
	sys, err := r.ReadSystem()
	if err != nil {
		return domain.SystemMemory{}, err
	}
	now := r.deps.Now()
	r.mu.Lock()
	last, lastAt := r.lastSystem, r.lastSystemAt
	r.lastSystem, r.lastSystemAt = sys, now
	r.mu.Unlock()
	out := domain.SystemMemory{
		TotalBytes: sys.TotalBytes, AvailableBytes: sys.AvailableBytes,
		SwapTotalBytes: sys.SwapTotalBytes, SwapUsedBytes: sys.SwapUsedBytes,
		CPUCount: sys.CPUCount, Load1: sys.Load1,
		PressureRaw: sys.PressureRaw, PressureSource: sys.PressureSource,
	}
	if gap := now.Sub(lastAt).Seconds(); !lastAt.IsZero() && gap > 0 && sys.SwapPages >= last.SwapPages {
		out.SwapBytesPerSec = float64(sys.SwapPages-last.SwapPages) * float64(sys.SwapPageBytes) / gap
	}
	switch {
	case !lastAt.IsZero() && sys.CPUTotalTicks > last.CPUTotalTicks && sys.CPUBusyTicks >= last.CPUBusyTicks:
		out.CPUPercent = 100 * float64(sys.CPUBusyTicks-last.CPUBusyTicks) / float64(sys.CPUTotalTicks-last.CPUTotalTicks)
		out.CPUMeasured = true
	case sys.CPUTotalTicks == 0:
		if table, prev, elapsed, err := r.table(ctx); err == nil && sys.CPUCount > 0 {
			out.CPUPercent = min(100, procmem.CPUPercent(table.All(), prev, elapsed)/float64(sys.CPUCount))
			out.CPUMeasured = prev != nil && elapsed > 0
		}
	}
	return out, nil
}

const cpuRateMaxGap = 3 * time.Minute

func (r *MemoryReader) AppMemory(ctx context.Context) (domain.AppMemory, error) {
	if r == nil || r.deps.Store == nil || r.deps.Runtime == nil {
		return domain.AppMemory{}, fmt.Errorf("session memory reader is unavailable")
	}
	recs, err := r.deps.Store.ListAllSessions(ctx)
	if err != nil {
		return domain.AppMemory{}, err
	}
	table, prev, elapsed, err := r.table(ctx)
	if err != nil {
		return domain.AppMemory{}, err
	}
	var own []int
	if r.deps.AppRootPIDs != nil {
		own = append(own, r.deps.AppRootPIDs()...)
	}
	roots := append([]int(nil), own...)
	inSession := map[int]bool{}
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		sessionRoots := r.rootPIDs(ctx, rec)
		roots = append(roots, sessionRoots...)
		for _, p := range table.Tree(sessionRoots...).Processes {
			inSession[p.PID] = true
		}
	}
	var reviewers []domain.ReviewerMemory
	inReviewer := map[int]bool{}
	if r.deps.Reviewers != nil {
		handles, err := r.deps.Reviewers.ListLiveReviewerHandles(ctx)
		if err != nil {
			return domain.AppMemory{}, err
		}
		for _, h := range handles {
			reviewerRoots, err := r.deps.Runtime.ProcessRootPIDs(ctx, ports.RuntimeHandle{ID: h.HandleID})
			if err != nil || len(reviewerRoots) == 0 {
				continue
			}
			reviewerTree := table.Tree(reviewerRoots...)
			if len(reviewerTree.Processes) == 0 {
				continue
			}
			roots = append(roots, reviewerRoots...)
			for _, p := range reviewerTree.Processes {
				inReviewer[p.PID] = true
			}
			reading := treeReading(reviewerTree, prev, elapsed)
			reading.SampledAt = r.deps.Now()
			reviewers = append(reviewers, domain.ReviewerMemory{
				ReviewID: h.ReviewID, SessionID: h.SessionID, Harness: h.Harness, Memory: reading,
			})
		}
	}
	tree := table.Tree(roots...)
	ownTree := table.Tree(own...)
	ownOnly := procmem.Tree{}
	for _, p := range ownTree.Processes {
		if inSession[p.PID] || inReviewer[p.PID] {
			continue
		}
		ownOnly.RSSBytes += p.RSSBytes
		ownOnly.CPUSeconds += p.CPUSeconds
		ownOnly.Processes = append(ownOnly.Processes, p)
	}
	ownReading := treeReading(ownOnly, prev, elapsed)
	ownReading.SampledAt = r.deps.Now()
	return domain.AppMemory{
		RSSBytes: tree.RSSBytes, ProcessCount: len(tree.Processes),
		CPUPercent: procmem.CPUPercent(tree.Processes, prev, elapsed), CPUMeasured: prev != nil && elapsed > 0,
		Own: ownReading, Reviewers: reviewers,
	}, nil
}

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

func (r *MemoryReader) table(ctx context.Context) (cur, prev *procmem.Table, elapsed float64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.deps.Now()
	if r.cached == nil || r.deps.CacheTTL == 0 || now.Sub(r.cachedAt) >= r.deps.CacheTTL {
		table, err := r.deps.Snapshot(ctx)
		if err != nil {
			if errors.Is(err, procmem.ErrUnsupported) {
				return nil, nil, 0, err
			}
			return nil, nil, 0, fmt.Errorf("sample process memory: %w", err)
		}
		r.prev, r.prevAt = r.cached, r.cachedAt
		r.cached, r.cachedAt = table, now
	}
	if r.prev != nil && r.cachedAt.Sub(r.prevAt) <= cpuRateMaxGap {
		elapsed = r.cachedAt.Sub(r.prevAt).Seconds()
	}
	return r.cached, r.prev, elapsed, nil
}
