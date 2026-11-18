// Package dirseed copies a plain folder, one with no git repository, for a
// session fork: a standalone agent's workspace.
//
// Capture copies the folder to a seed folder, with copy-on-write where the
// filesystem has it, so the source is never touched. Apply moves the seed's
// files into the fork's new, empty workspace.
package dirseed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DirName is the folder, under the data directory, that holds every seed.
// Leftovers here with no fork are reaped.
const DirName = "fork-seeds"

// ErrCloneUnsupported means the filesystem can't clone a folder; the caller
// copies it instead.
var ErrCloneUnsupported = errors.New("copy-on-write clone is not supported")

// cloneTree is CloneTree; tests replace it to cover the copy fallback.
var cloneTree = CloneTree

// Capture copies workspace to seed, which must not exist yet. Regular files,
// folders and symlinks are copied; a symlink stays a link and its target is
// never read. On any failure, including cancellation, seed is removed.
func Capture(ctx context.Context, workspace, seed string) (err error) {
	if strings.TrimSpace(seed) == "" {
		return errors.New("dirseed: seed path is required")
	}
	src, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return fmt.Errorf("dirseed: resolve workspace: %w", err)
	}
	if info, err := os.Stat(src); err != nil {
		return fmt.Errorf("dirseed: workspace: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("dirseed: workspace %q is not a folder", workspace)
	}
	if _, err := os.Lstat(seed); err == nil {
		return fmt.Errorf("dirseed: seed %q already exists", seed)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("dirseed: seed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(seed), 0o750); err != nil {
		return fmt.Errorf("dirseed: create seed folder: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(seed)
		}
	}()
	err = cloneTree(src, seed)
	if errors.Is(err, ErrCloneUnsupported) {
		err = copyTree(ctx, src, seed)
	}
	if err != nil {
		return fmt.Errorf("dirseed: copy workspace: %w", err)
	}
	// A clone keeps the source folder's times; the reaper reads the seed's
	// own to tell a leftover from one just captured.
	now := time.Now()
	if err := os.Chtimes(seed, now, now); err != nil {
		return fmt.Errorf("dirseed: stamp seed: %w", err)
	}
	return nil
}

// Apply moves the seed's files into workspace, which must be an existing,
// empty folder, then removes the seed. Files that can't be moved, across
// volumes, are copied.
func Apply(ctx context.Context, seed, workspace string) error {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return fmt.Errorf("dirseed: workspace: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("dirseed: workspace %q is not empty", workspace)
	}
	entries, err = os.ReadDir(seed)
	if err != nil {
		return fmt.Errorf("dirseed: seed: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		from, to := filepath.Join(seed, entry.Name()), filepath.Join(workspace, entry.Name())
		if err := os.Rename(from, to); err == nil {
			continue
		}
		if err := copyEntry(ctx, from, to); err != nil {
			return fmt.Errorf("dirseed: copy %s: %w", entry.Name(), err)
		}
	}
	return Delete(seed)
}

// Delete removes a seed. A missing seed is not an error.
func Delete(seed string) error {
	if strings.TrimSpace(seed) == "" {
		return errors.New("dirseed: seed path is required")
	}
	if err := os.RemoveAll(seed); err != nil {
		return fmt.Errorf("dirseed: remove seed: %w", err)
	}
	return nil
}

// Reap removes seeds under root last changed more than maxAge before now. A
// seed lives only while its fork is being made, so an older one was left by a
// crash. A missing root has nothing to reap.
func Reap(root string, maxAge time.Duration, now time.Time) (int, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("dirseed: list seeds: %w", err)
	}
	removed := 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) <= maxAge {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return removed, fmt.Errorf("dirseed: remove seed %s: %w", entry.Name(), err)
		}
		removed++
	}
	return removed, nil
}

// copyEntry copies one file, link or folder tree from to to, then removes from.
func copyEntry(ctx context.Context, from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if info.IsDir() {
		err = copyTree(ctx, from, to)
	} else {
		err = copyOne(from, to, info)
	}
	if err != nil {
		return err
	}
	return os.RemoveAll(from)
}

// copyTree copies src's folders, regular files and symlinks to dst. Other
// kinds of file, such as sockets, are left out.
func copyTree(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		return copyOne(path, target, info)
	})
}

// copyOne copies a regular file, keeping its permissions, or recreates a
// symlink. Anything else is skipped.
func copyOne(from, to string, info fs.FileInfo) error {
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(from)
		if err != nil {
			return err
		}
		return os.Symlink(target, to)
	case info.Mode().IsRegular():
		in, err := os.Open(from)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	default:
		return nil
	}
}
