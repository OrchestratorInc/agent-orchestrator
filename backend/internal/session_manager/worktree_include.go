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

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// copyWorktreeInclude copies the files the project's .worktreeinclude names
// into the workspace. Claude Code and the Codex app apply this file only to
// worktrees they create themselves, and AO creates session worktrees, so AO
// has to apply it. As in those tools, a file is copied only when it is also
// gitignored, so tracked files and ordinary untracked work are never
// duplicated. Existing workspace files and anything but regular files are
// left alone.
func copyWorktreeInclude(ctx context.Context, project domain.ProjectRecord, workspacePath string, workspace ports.Workspace) error {
	projectPath := project.Path
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
	// Configured directory symlinks can point back into the project. Keep
	// their existing files; only missing paths are checked and copied.
	var missing []string
	for _, rel := range strings.Split(string(ignored), "\x00") {
		if rel == "" {
			continue
		}
		clean, err := safeRelPath(rel)
		if err != nil {
			return fmt.Errorf(".worktreeinclude %q: %w", rel, err)
		}
		if _, err := os.Lstat(filepath.Join(workspacePath, clean)); err == nil {
			continue
		}
		missing = append(missing, rel)
	}
	if len(missing) == 0 {
		return nil
	}
	// The workspace's revision can lack the source checkout's ignore rule (an
	// uncommitted .gitignore edit, an older base), and a copy it does not
	// ignore would be committed by the agent's next `git add -A`.
	ignoredHere, err := worktreeIncludeGit(ctx, workspacePath, []byte(strings.Join(missing, "\x00")), "check-ignore", "-z", "--stdin")
	if err != nil || len(ignoredHere) == 0 {
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
	type includedCopy struct{ rel, temporary string }
	var copies []includedCopy
	var gitPaths []string
	for _, rel := range strings.Split(string(ignoredHere), "\x00") {
		if rel == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		clean, err := safeRelPath(rel)
		if err != nil {
			return fmt.Errorf(".worktreeinclude %q: %w", rel, err)
		}
		info, err := source.Lstat(clean)
		if err != nil {
			return fmt.Errorf(".worktreeinclude %q: %w", clean, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		tmp := ".ao-include-" + rand.Text()
		copies = append(copies, includedCopy{clean, tmp})
		gitPaths = append(gitPaths, filepath.ToSlash(clean), filepath.ToSlash(filepath.Join(filepath.Dir(clean), tmp)))
	}
	if len(copies) == 0 {
		return nil
	}
	if workspace == nil {
		return errors.New(".worktreeinclude: workspace file protection is unavailable")
	}
	// A stopped daemon cannot remove its temporary copies. Keep them out of
	// Git even when the worktree is resumed without provisioning again.
	if err := workspace.AddExclude(ctx, ports.WorkspaceInfo{ProjectID: domain.ProjectID(project.ID), Path: workspacePath}, ".ao-include-*"); err != nil {
		return fmt.Errorf(".worktreeinclude: protect temporary copies: %w", err)
	}
	protected, err := worktreeIncludeGit(ctx, workspacePath, []byte(strings.Join(gitPaths, "\x00")+"\x00"), "check-ignore", "-z", "--stdin")
	if err != nil {
		return err
	}
	ignoredPaths := make(map[string]bool)
	for _, path := range strings.Split(string(protected), "\x00") {
		ignoredPaths[path] = true
	}
	for _, path := range gitPaths {
		if !ignoredPaths[path] {
			return fmt.Errorf(".worktreeinclude %q: the session does not ignore this path; add it to the branch's .gitignore, then try again", path)
		}
	}
	for _, item := range copies {
		if err := copyNewRegularFile(ctx, source, target, item.rel, item.temporary); err != nil {
			return fmt.Errorf(".worktreeinclude %q: %w", item.rel, err)
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
func copyNewRegularFile(ctx context.Context, source, target *os.Root, rel, temporary string) error {
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
	return publishWorktreeIncludeFile(target, rel, contextReader{ctx, src}, info.Mode().Perm(), temporary)
}

// contextReader stops a copy of a large file once the spawn is cancelled.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func publishWorktreeIncludeFile(target *os.Root, rel string, src io.Reader, mode fs.FileMode, tmp string) error {
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
