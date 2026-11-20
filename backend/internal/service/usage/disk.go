package usage

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// worktreeSizeTTL is how long a measured size is served before the worktree is
// walked again. A worktree with installed dependencies takes seconds to walk.
const worktreeSizeTTL = 5 * time.Minute

// DiskReaderDeps wires the disk reader.
type DiskReaderDeps struct {
	Store memorySessionStore
	// ManagedRoot is the directory session worktrees are created under. A
	// session whose workspace is anywhere else runs in the user's own
	// checkout, which is never measured.
	ManagedRoot string
	// Measure sizes one directory tree. Defaults to walking it.
	Measure func(ctx context.Context, root string) int64
	Now     func() time.Time
	// TTL defaults to worktreeSizeTTL.
	TTL time.Duration
}

type worktreeSize struct {
	bytes      int64
	measuredAt time.Time
}

// DiskReader reports the size of every live session's worktree. Sizes are
// measured by Run in the background and cached by worktree path; ListDisk
// only reads the cache.
type DiskReader struct {
	deps  DiskReaderDeps
	roots []string
	wake  chan struct{}

	mu    sync.Mutex
	sizes map[string]worktreeSize
}

// NewDiskReader constructs a disk reader. Nothing is measured until Run is
// started and ListDisk is called.
func NewDiskReader(deps DiskReaderDeps) *DiskReader {
	if deps.Measure == nil {
		deps.Measure = treeBytes
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.TTL <= 0 {
		deps.TTL = worktreeSizeTTL
	}
	return &DiskReader{
		deps:  deps,
		roots: managedRoots(deps.ManagedRoot),
		wake:  make(chan struct{}, 1),
		sizes: map[string]worktreeSize{},
	}
}

// managedRoots is root as configured and with its symlinks resolved: the
// workspace adapters record resolved paths, and a data dir under /tmp or a
// test's temp dir is behind a symlink on macOS.
func managedRoots(root string) []string {
	root = filepath.Clean(root)
	roots := []string{root}
	physical, err := filepath.EvalSymlinks(root)
	if err != nil {
		// Not created until the first session: resolve the data dir above it.
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(root))
		if parentErr != nil {
			return roots
		}
		physical = filepath.Join(parent, filepath.Base(root))
	}
	if physical != root {
		roots = append(roots, physical)
	}
	return roots
}

// worktree is the path to measure for rec, or "" when it has none: the session
// is terminated, is a hidden task preparation, or runs outside the managed root.
func (r *DiskReader) worktree(rec domain.SessionRecord) string {
	if rec.IsTerminated || rec.IsTaskPreparation || rec.Metadata.WorkspacePath == "" {
		return ""
	}
	path := filepath.Clean(rec.Metadata.WorkspacePath)
	for _, root := range r.roots {
		if rel, err := filepath.Rel(root, path); err == nil && rel != "." && filepath.IsLocal(rel) {
			return path
		}
	}
	return ""
}

// ListDisk returns the cached size of each live session's worktree and wakes
// Run when any is missing or older than the TTL. It never touches the file
// system. A worktree that is not measured yet, or is not on disk, has no row.
func (r *DiskReader) ListDisk(ctx context.Context, projectID domain.ProjectID) (domain.DiskReport, error) {
	if r == nil || r.deps.Store == nil {
		return domain.DiskReport{}, fmt.Errorf("session disk reader is unavailable")
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
		return domain.DiskReport{}, err
	}
	now := r.deps.Now()
	report := domain.DiskReport{Sessions: make([]domain.SessionDisk, 0, len(recs))}
	counted := map[string]bool{}
	r.mu.Lock()
	for _, rec := range recs {
		path := r.worktree(rec)
		if path == "" {
			continue
		}
		size, measured := r.sizes[path]
		first := !counted[path]
		counted[path] = true
		if first && (!measured || now.Sub(size.measuredAt) >= r.deps.TTL) {
			report.Measuring++
		}
		if !measured || size.bytes == 0 {
			continue
		}
		report.Sessions = append(report.Sessions, domain.SessionDisk{SessionID: rec.ID, SizeBytes: size.bytes, MeasuredAt: size.measuredAt})
		if first {
			report.TotalBytes += size.bytes
		}
	}
	r.mu.Unlock()
	if report.Measuring > 0 {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
	return report, nil
}

// Run measures worktrees each time ListDisk asks, until ctx is done. This is
// the only place a worktree is walked, and it walks one at a time.
func (r *DiskReader) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
			r.refresh(ctx)
		}
	}
}

// refresh measures every worktree whose size is missing or older than the
// TTL, and forgets the ones no live session has any more.
func (r *DiskReader) refresh(ctx context.Context) {
	recs, err := r.deps.Store.ListAllSessions(ctx)
	if err != nil {
		// ListDisk reports a store failure to its caller and wakes this again.
		return
	}
	live := map[string]bool{}
	var paths []string
	for _, rec := range recs {
		if path := r.worktree(rec); path != "" && !live[path] {
			live[path] = true
			paths = append(paths, path)
		}
	}
	r.mu.Lock()
	for path := range r.sizes {
		if !live[path] {
			delete(r.sizes, path)
		}
	}
	r.mu.Unlock()
	for _, path := range paths {
		r.mu.Lock()
		size, measured := r.sizes[path]
		r.mu.Unlock()
		if measured && r.deps.Now().Sub(size.measuredAt) < r.deps.TTL {
			continue
		}
		bytes := r.deps.Measure(ctx, path)
		if ctx.Err() != nil {
			// A cancelled walk stopped short: keep the size from before.
			return
		}
		r.mu.Lock()
		r.sizes[path] = worktreeSize{bytes: bytes, measuredAt: r.deps.Now()}
		r.mu.Unlock()
	}
}

// treeBytes is the disk space under root as du counts it. Symlinks are not
// followed, and an entry that cannot be read is skipped, so a root that does
// not exist is zero.
func treeBytes(ctx context.Context, root string) int64 {
	var total int64
	links := map[[2]any]struct{}{}
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is skipped
		}
		info, err := entry.Info()
		if err != nil {
			return nil //nolint:nilerr // removed while walking
		}
		total += allocatedBytes(info, links)
		return nil
	})
	return total
}
