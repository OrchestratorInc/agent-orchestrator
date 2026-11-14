package sessionmanager

import (
	"bytes"
	"context"
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
	// ponytail: ls-files walks every untracked directory, so a huge ignored
	// tree such as node_modules slows each spawn; prune with --directory if it shows up.
	matched, err := worktreeIncludeGit(ctx, projectPath, nil, "ls-files", "-z", "--others", "--ignored", "--exclude-from="+includeFile)
	if err != nil || len(matched) == 0 {
		return err
	}
	ignored, err := worktreeIncludeGit(ctx, projectPath, matched, "check-ignore", "-z", "--stdin")
	if err != nil {
		return err
	}
	for _, rel := range strings.Split(string(ignored), "\x00") {
		if rel == "" {
			continue
		}
		clean, err := safeRelPath(rel)
		if err != nil {
			return fmt.Errorf(".worktreeinclude %q: %w", rel, err)
		}
		if err := copyNewRegularFile(filepath.Join(projectPath, clean), filepath.Join(workspacePath, clean)); err != nil {
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
func copyNewRegularFile(source, target string) (err error) {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dst.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(target)
		}
	}()
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	_, err = io.Copy(dst, src)
	return err
}
