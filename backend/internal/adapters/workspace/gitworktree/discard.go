package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const discardedDirName = ".discarded"

func (w *Workspace) discardedRoot() string {
	return filepath.Join(w.managedRoot, discardedDirName)
}

func (w *Workspace) discard(path string) (string, bool) {
	root := w.discardedRoot()
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", false
	}
	base := filepath.Base(path)
	for i := range 100 {
		candidate := filepath.Join(root, fmt.Sprintf("%s-%d-%d", base, time.Now().UnixNano(), i))
		if _, err := os.Lstat(candidate); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false
		}
		if err := os.Rename(path, candidate); err != nil {
			return "", false
		}
		return candidate, true
	}
	return "", false
}

func (w *Workspace) undiscard(discarded, path string) {
	if err := os.Rename(discarded, path); err != nil {
		w.logf("gitworktree: could not restore discarded worktree %q to %q: %v", discarded, path, err)
	}
}

func (w *Workspace) removeInBackground(path string) {
	w.discards.Add(1)
	go func() {
		defer w.discards.Done()
		if err := removeAllWithRetry(context.Background(), path); err != nil {
			w.logf("gitworktree: could not remove discarded worktree %q: %v", path, err)
		}
	}()
}

func (w *Workspace) sweepDiscarded() {
	entries, err := os.ReadDir(w.discardedRoot())
	if err != nil {
		return
	}
	for _, entry := range entries {
		w.removeInBackground(filepath.Join(w.discardedRoot(), entry.Name()))
	}
}

func (w *Workspace) waitForDiscards() {
	w.discards.Wait()
}

func (w *Workspace) logf(format string, args ...any) {
	if w.logger == nil {
		return
	}
	w.logger.Warn(fmt.Sprintf(format, args...))
}

func (w *Workspace) worktreeRegistration(ctx context.Context, repo, path string) (registered, locked, conclusive bool) {
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return false, false, false
	}
	rec, found := findWorktree(records, path)
	return found, found && rec.Locked, true
}

func (w *Workspace) worktreeDirty(ctx context.Context, path string) (dirty, conclusive bool) {
	dirty, err := w.isDirty(ctx, path)
	if err != nil {
		return false, false
	}
	return dirty, true
}

func (w *Workspace) discardWorktree(ctx context.Context, repo, path string) (bool, error) {
	registered, locked, conclusive := w.worktreeRegistration(ctx, repo, path)
	if !conclusive || locked {
		return false, nil
	}
	discarded, moved := w.discard(path)
	if !moved {
		return false, nil
	}
	if registered {
		dirty, conclusive := w.worktreeDirty(ctx, discarded)
		if !conclusive {
			w.undiscard(discarded, path)
			return false, nil
		}
		if dirty {
			w.undiscard(discarded, path)
			return true, fmt.Errorf("gitworktree: refusing to remove %q: %w (worktree has uncommitted changes)", path, ports.ErrWorkspaceDirty)
		}
	}
	if _, err := w.run(ctx, w.binary, worktreePruneArgs(repo)...); err != nil {
		w.undiscard(discarded, path)
		return false, nil //nolint:nilerr // the git-driven path re-runs prune and reports the failure
	}
	stillRegistered, _, conclusive := w.worktreeRegistration(ctx, repo, path)
	if !conclusive {
		w.removeInBackground(discarded)
		return true, nil
	}
	if stillRegistered {
		w.undiscard(discarded, path)
		return true, fmt.Errorf("gitworktree: refusing to remove %q: path is still registered after git worktree prune", path)
	}
	w.removeInBackground(discarded)
	return true, nil
}
