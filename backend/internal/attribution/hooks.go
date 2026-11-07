package attribution

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

//go:embed hook.sh
var hookScript []byte

// HookNames are the client-side hooks Git runs from core.hooksPath. Each gets a
// copy of the dispatcher so the repository's own hook of that name still runs.
// fsmonitor-watchman is left out: Git reads it from core.fsmonitor, not the
// hooks directory.
var HookNames = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch",
	"pre-commit", "pre-merge-commit", "prepare-commit-msg", "commit-msg", "post-commit",
	"pre-rebase", "post-checkout", "post-merge", "pre-push",
	"pre-receive", "update", "proc-receive", "post-receive", "post-update",
	"reference-transaction", "push-to-checkout", "pre-auto-gc", "post-rewrite",
	"sendemail-validate", "post-index-change",
	"p4-changelist", "p4-prepare-changelist", "p4-post-changelist", "p4-pre-submit",
}

const (
	// ConfigCoAuthor is the worktree-scoped git config key the hook reads the
	// trailer value from. Unset means no trailer.
	ConfigCoAuthor = "orchestrator.coauthor"
	// ConfigOriginalHooksPath records a core.hooksPath that this worktree's own
	// config set before Orchestrator replaced it, so the dispatcher can still
	// run those hooks and RemoveFromWorktree can put it back.
	ConfigOriginalHooksPath = "orchestrator.originalHooksPath"
)

// HooksDir is where the hook dispatcher lives under the daemon data dir.
func HooksDir(dataDir string) string { return filepath.Join(dataDir, "git-hooks") }

var installMu sync.Mutex

// InstallHooks writes the dispatcher under dir for every hook name. Files that
// already hold the current script are left alone, so concurrent spawns and
// restarts do not rewrite them.
func InstallHooks(dir string) error {
	installMu.Lock()
	defer installMu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("attribution: create hooks dir: %w", err)
	}
	for _, name := range HookNames {
		path := filepath.Join(dir, name)
		if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, hookScript) {
			if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o111 != 0 {
				continue
			}
		}
		tmp, err := os.CreateTemp(dir, "."+name+".*")
		if err != nil {
			return fmt.Errorf("attribution: write %s hook: %w", name, err)
		}
		_, writeErr := tmp.Write(hookScript)
		closeErr := tmp.Close()
		if err := errors.Join(writeErr, closeErr, os.Chmod(tmp.Name(), 0o755)); err != nil {
			_ = os.Remove(tmp.Name())
			return fmt.Errorf("attribution: write %s hook: %w", name, err)
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			_ = os.Remove(tmp.Name())
			return fmt.Errorf("attribution: install %s hook: %w", name, err)
		}
	}
	return nil
}

// ConfigureWorktree points one worktree's hooks at hooksDir and sets the
// trailer the hook adds. Everything is written with `git config --worktree`, so
// the person's own checkout and other worktrees are untouched. It needs
// extensions.worktreeConfig, which session worktrees already enable.
//
// An empty coAuthor still routes hooks through the dispatcher (which then only
// passes through) and clears any trailer an earlier session configured.
func ConfigureWorktree(ctx context.Context, worktree, hooksDir, coAuthor string) error {
	git := func(args ...string) (string, error) {
		cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", worktree}, args...)...)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	current, _ := git("config", "--worktree", "--get", "core.hooksPath")
	if current == "" || filepath.Clean(current) != filepath.Clean(hooksDir) {
		// A hooksPath set in this worktree's own config would be hidden by
		// ours, so it is saved and the dispatcher runs it. Repository, global
		// and system values are read live by the dispatcher instead, which
		// also covers a hooksPath that husky sets after the session started.
		if current != "" {
			if out, err := git("config", "--worktree", ConfigOriginalHooksPath, current); err != nil {
				return fmt.Errorf("attribution: save original hooksPath: %s: %w", out, err)
			}
		}
		if out, err := git("config", "--worktree", "core.hooksPath", hooksDir); err != nil {
			return fmt.Errorf("attribution: set hooksPath: %s: %w", out, err)
		}
	}
	if coAuthor == "" {
		if out, err := git("config", "--worktree", "--unset-all", ConfigCoAuthor); err != nil && !isMissingKey(err) {
			return fmt.Errorf("attribution: clear co-author: %s: %w", out, err)
		}
		return nil
	}
	if out, err := git("config", "--worktree", ConfigCoAuthor, coAuthor); err != nil {
		return fmt.Errorf("attribution: set co-author: %s: %w", out, err)
	}
	return nil
}

// RemoveFromWorktree undoes ConfigureWorktree, restoring a worktree-scoped
// hooksPath it saved. The repository's own config is never touched.
func RemoveFromWorktree(ctx context.Context, worktree string) error {
	git := func(args ...string) (string, error) {
		cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", worktree, "config", "--worktree"}, args...)...)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	original, _ := git("--get", ConfigOriginalHooksPath)
	for _, key := range []string{"core.hooksPath", ConfigCoAuthor, ConfigOriginalHooksPath} {
		if out, err := git("--unset-all", key); err != nil && !isMissingKey(err) {
			return fmt.Errorf("attribution: unset %s: %s: %w", key, out, err)
		}
	}
	if original != "" {
		if out, err := git("core.hooksPath", original); err != nil {
			return fmt.Errorf("attribution: restore hooksPath: %s: %w", out, err)
		}
	}
	return nil
}

// isMissingKey reports git config's exit status 5: the key was not set.
func isMissingKey(err error) bool {
	var exitErr interface{ ExitCode() int }
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 5
}
