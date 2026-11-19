package sessionmanager

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// forkDependencyDirs are the dependency folders a fork clones from its
// original instead of installing again. They are git-ignored, so the worktree
// copy never has them.
var forkDependencyDirs = map[string]bool{"node_modules": true, ".venv": true}

// forkDependencyDepth bounds how deep a monorepo's packages are searched.
const forkDependencyDepth = 4

var errCloneUnsupported = errors.New("copy-on-write clone is not supported")

// cloneForkDependencies clones the original's dependency folders into the
// fork with copy-on-write, so a large node_modules costs almost no time or
// disk. A folder the fork already has is left alone. Where clones aren't
// supported it returns errCloneUnsupported, and setup installs as usual.
func cloneForkDependencies(ctx context.Context, src, dst string) ([]string, error) {
	var cloned []string
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip an unreadable folder; setup installs what's missing
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if !entry.IsDir() || path == src {
			return nil
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return nil //nolint:nilerr // skip a path outside the source
		}
		if entry.Name() == ".git" || strings.Count(rel, string(filepath.Separator)) >= forkDependencyDepth {
			return filepath.SkipDir
		}
		if !forkDependencyDirs[entry.Name()] {
			return nil
		}
		target := filepath.Join(dst, rel)
		if _, statErr := os.Lstat(target); statErr == nil {
			return filepath.SkipDir
		}
		if _, statErr := os.Stat(filepath.Dir(target)); statErr != nil {
			// The fork has no such package folder; nothing to put it in.
			return filepath.SkipDir
		}
		if cloneErr := cloneTree(path, target); cloneErr != nil {
			return cloneErr
		}
		cloned = append(cloned, rel)
		return filepath.SkipDir
	})
	return cloned, err
}

// markForkWorkspace makes the next setup of path run with AO_FORK=1.
func (m *Manager) markForkWorkspace(path string) { m.forkWorkspaces.Store(filepath.Clean(path), true) }

// takeForkWorkspace reports, once, whether path belongs to a new fork.
func (m *Manager) takeForkWorkspace(path string) bool {
	_, ok := m.forkWorkspaces.LoadAndDelete(filepath.Clean(path))
	return ok
}
