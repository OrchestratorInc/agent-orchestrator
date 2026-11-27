package scratch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// stagedAgentStateDir holds, under the managed root, the agent state a
// deleting teardown moved out of a workspace until the delete commits. Project
// IDs start with a letter or digit, so it never collides with a project's
// directory, and it shares the workspaces' filesystem, so moves are renames.
const stagedAgentStateDir = ".ao-staged-agent-state"

// RestoreAgentState puts the agent state a deleting teardown staged back into
// the workspace, for a delete that did not go through, and reports whether
// there was any.
func (w *Workspace) RestoreAgentState(_ context.Context, info ports.WorkspaceInfo) (bool, error) {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return false, err
	}
	staging, err := w.stagingPath(path)
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(staging); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return true, w.restoreStagedAgentState(path)
}

// DiscardAgentState removes the agent state a deleting teardown staged, once
// the session it belonged to is deleted.
func (w *Workspace) DiscardAgentState(_ context.Context, info ports.WorkspaceInfo) error {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return err
	}
	staging, err := w.stagingPath(path)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("scratch workspace: discard staged agent state %q: %w", staging, err)
	}
	w.pruneStagingParents(filepath.Dir(staging))
	return nil
}

func (w *Workspace) stagingRoot() string {
	return filepath.Join(w.managedRoot, stagedAgentStateDir)
}

// stagingPath is where workspace's agent state is staged: its path under the
// managed root, mirrored under the staging root.
func (w *Workspace) stagingPath(workspace string) (string, error) {
	rel, err := filepath.Rel(w.managedRoot, workspace)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("scratch workspace: %q is outside the managed root: %w", workspace, ports.ErrWorkspaceUnsafePath)
	}
	return filepath.Join(w.stagingRoot(), rel), nil
}

// stageAgentState moves each scanned state directory to the same relative
// path under workspace's staging directory. State left staged by an earlier
// delete that never settled is put back instead, and the teardown is refused
// so the next attempt scans everything that is in the workspace again.
func (w *Workspace) stageAgentState(workspace string, stateDirs []string) error {
	if len(stateDirs) == 0 {
		return nil
	}
	staging, err := w.stagingPath(workspace)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(staging); err == nil {
		if err := w.restoreStagedAgentState(workspace); err != nil {
			return err
		}
		return fmt.Errorf("scratch workspace: restored agent state an earlier delete left staged for %q: %w", workspace, ports.ErrWorkspaceDirty)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("scratch workspace: inspect staged agent state %q: %w", staging, err)
	}
	for _, dir := range stateDirs {
		rel, err := filepath.Rel(workspace, dir)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("scratch workspace: agent state %q is outside %q: %w", dir, workspace, ports.ErrWorkspaceUnsafePath)
		}
		target := filepath.Join(staging, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("scratch workspace: stage agent state %q: %w", dir, err)
		}
		if err := os.Rename(dir, target); err != nil {
			return fmt.Errorf("scratch workspace: stage agent state %q: %w", dir, err)
		}
	}
	return nil
}

// restoreStagedAgentState moves everything staged for workspace back,
// recreating the workspace's directories. A staged directory whose place was
// taken meanwhile is merged entry by entry; an entry that would overwrite one
// stays staged and is reported, never replaced.
func (w *Workspace) restoreStagedAgentState(workspace string) error {
	staging, err := w.stagingPath(workspace)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(staging); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("scratch workspace: inspect staged agent state %q: %w", staging, err)
	}
	var errs []error
	for _, rel := range hookutil.RestorableStateDirs() {
		staged := filepath.Join(staging, filepath.FromSlash(rel))
		if _, err := os.Lstat(staged); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			errs = append(errs, err)
			continue
		}
		target := filepath.Join(workspace, filepath.FromSlash(rel))
		if err := moveBack(staged, target); err != nil {
			errs = append(errs, err)
			continue
		}
		// Teardown may have removed the marker that made this state AO's.
		// Without one the state reads as user work; a fresh marker claims only
		// it, and never replaces a .gitignore that is already there.
		parent := filepath.Dir(target)
		if _, err := os.Lstat(filepath.Join(parent, ".gitignore")); errors.Is(err, os.ErrNotExist) {
			if err := hookutil.EnsureWorkspaceGitignore(parent, filepath.Base(target)+"/"); err != nil {
				errs = append(errs, err)
			}
		}
	}
	removeEmptyDirs(staging)
	w.pruneStagingParents(filepath.Dir(staging))
	if len(errs) > 0 {
		return fmt.Errorf("scratch workspace: restore staged agent state for %q: %w", workspace, errors.Join(errs...))
	}
	return nil
}

func moveBack(staged, target string) error {
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		return os.Rename(staged, target)
	} else if err != nil {
		return err
	}
	entries, err := os.ReadDir(staged)
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		dst := filepath.Join(target, entry.Name())
		if _, err := os.Lstat(dst); err == nil {
			errs = append(errs, fmt.Errorf("%q already exists", dst))
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		if err := os.Rename(filepath.Join(staged, entry.Name()), dst); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// removeEmptyDirs removes root and the directories under it that are empty,
// children first, leaving anything that still holds a file.
func removeEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
}

// pruneStagingParents removes now-empty directories from dir up to and
// including the staging root.
func (w *Workspace) pruneStagingParents(dir string) {
	root := w.stagingRoot()
	for {
		if rel, err := filepath.Rel(root, dir); err != nil || !(rel == "." || filepath.IsLocal(rel)) {
			return
		}
		if os.Remove(dir) != nil || dir == root {
			return
		}
		dir = filepath.Dir(dir)
	}
}
