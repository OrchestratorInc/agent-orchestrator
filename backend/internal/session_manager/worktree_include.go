package sessionmanager

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// copyWorktreeInclude copies the files the project's .worktreeinclude names
// into the workspace. Claude Code and the Codex app apply this file only to
// worktrees they create themselves, and AO creates session worktrees, so AO
// has to apply it. As in those tools, a file is copied only when it is also
// gitignored, so tracked files and ordinary untracked work are never
// duplicated. Existing workspace files and anything but regular files are
// left alone.
func copyWorktreeInclude(ctx context.Context, projectPath, workspacePath string) error {
	includeFile := filepath.Join(projectPath, ".worktreeinclude")
	if _, err := os.Lstat(includeFile); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	matched, err := worktreeIncludeGit(ctx, projectPath, nil, "ls-files", "-z", "--others", "--ignored", "--exclude-from="+includeFile)
	if err != nil || len(matched) == 0 {
		return err
	}
	ignored, err := worktreeIncludeGit(ctx, projectPath, matched, "check-ignore", "-z", "--stdin")
	if err != nil {
		return err
	}
	source, err := os.OpenRoot(projectPath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	target, err := os.OpenRoot(workspacePath)
	if err != nil {
		return err
	}
	defer func() { _ = target.Close() }()
	for _, rel := range strings.Split(string(ignored), "\x00") {
		if rel == "" {
			continue
		}
		clean, err := safeRelPath(rel)
		if err != nil {
			return fmt.Errorf(".worktreeinclude %q: %w", rel, err)
		}
		// Configured directory symlinks can point back into the project.
		// Keep their existing files, but confine every new write with os.Root.
		if _, err := os.Lstat(filepath.Join(workspacePath, clean)); err == nil {
			continue
		}
		if err := copyNewRegularFile(source, target, clean); err != nil {
			return fmt.Errorf(".worktreeinclude %q: %w", rel, err)
		}
	}
	return nil
}

func worktreeIncludeGit(ctx context.Context, projectPath string, stdin []byte, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", projectPath}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	var exit *exec.ExitError
	// check-ignore exits 1 when none of the paths is ignored.
	if errors.As(err, &exit) && exit.ExitCode() == 1 && args[0] == "check-ignore" {
		return nil, nil
	}
	if err != nil {
		if exit != nil {
			return nil, fmt.Errorf(".worktreeinclude: git %s: %w: %s", args[0], err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf(".worktreeinclude: git %s: %w", args[0], err)
	}
	return out, nil
}

// copyNewRegularFile copies source to target with source's permissions. It
// skips a source that is not a regular file and a target that already exists,
// including a link applySymlinks placed there.
func copyNewRegularFile(source, target *os.Root, rel string) error {
	info, err := source.Lstat(rel)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	src, err := source.Open(rel)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	return publishWorktreeIncludeFile(target, rel, src, info.Mode().Perm())
}

func publishWorktreeIncludeFile(target *os.Root, rel string, src io.Reader, mode fs.FileMode) error {
	if _, err := target.Lstat(rel); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := target.MkdirAll(filepath.Dir(rel), 0o750); err != nil {
		return err
	}
	dir, err := target.OpenRoot(filepath.Dir(rel))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	tmp := ".ao-include-" + rand.Text()
	dst, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Remove(tmp) }()
	defer func() { _ = dst.Close() }()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	if err := dst.Chmod(mode); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	// A hard link publishes the complete copy without replacing a file
	// created while we were copying. A stopped daemon leaves no partial destination.
	err = dir.Link(tmp, filepath.Base(rel))
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	return err
}
