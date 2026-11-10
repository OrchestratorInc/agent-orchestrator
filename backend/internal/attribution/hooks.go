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

// HookNames are the hooks every session worktree gets a dispatcher for: the
// commit-message hooks that add the trailer, plus the hooks repositories
// commonly set up (husky installs this set), so a hook the repository adds
// after the session started still runs. Git runs a hook only when its file
// exists, so the set stays small: each dispatcher costs a shell and a few git
// processes.
var HookNames = []string{
	"pre-commit", "pre-merge-commit", "prepare-commit-msg", "commit-msg", "post-commit",
	"applypatch-msg", "pre-applypatch", "post-applypatch",
	"pre-rebase", "post-rewrite", "post-checkout", "post-merge", "pre-push", "pre-auto-gc",
}

// onDemandHookNames run often (reference-transaction on every ref update,
// post-index-change on every index write) or rarely matter in a session. A
// dispatcher for one is installed only once a repository being configured
// has that hook, so worktrees of other repositories don't pay for it.
// fsmonitor-watchman is left out: Git reads it from core.fsmonitor, not the
// hooks directory.
var onDemandHookNames = []string{
	"reference-transaction", "post-index-change", "push-to-checkout", "sendemail-validate",
	"pre-receive", "update", "proc-receive", "post-receive", "post-update",
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

// InstallHooks writes the dispatcher under dir for every name in HookNames.
// Files that already hold the current script are left alone, so concurrent
// spawns and restarts do not rewrite them.
func InstallHooks(dir string) error {
	installMu.Lock()
	defer installMu.Unlock()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("attribution: create hooks dir: %w", err)
	}
	for _, name := range HookNames {
		if err := installHook(dir, name); err != nil {
			return err
		}
	}
	return nil
}

func installHook(dir, name string) error {
	path := filepath.Join(dir, name)
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, hookScript) {
		if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o111 != 0 {
			return nil
		}
	}
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return fmt.Errorf("attribution: write %s hook: %w", name, err)
	}
	_, writeErr := tmp.Write(hookScript)
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr, os.Chmod(tmp.Name(), 0o700)); err != nil { //nolint:gosec // executable git hook
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("attribution: write %s hook: %w", name, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("attribution: install %s hook: %w", name, err)
	}
	return nil
}

// installRepositoryHooks adds a dispatcher for each on-demand hook that one of
// the worktree's own hook directories holds, so the repository's hook still
// runs. The directories are the ones the dispatcher hands off to.
func installRepositoryHooks(worktree, hooksDir string, git func(...string) (string, error)) error {
	var dirs []string
	if saved, _ := git("config", "--worktree", "--type=path", "--get", ConfigOriginalHooksPath); saved != "" {
		dirs = append(dirs, saved)
	}
	if out, _ := git("config", "--type=path", "--show-scope", "--get-all", "core.hooksPath"); out != "" {
		for _, line := range strings.Split(out, "\n") {
			if scope, value, ok := strings.Cut(line, "\t"); ok && scope != "worktree" && value != "" {
				dirs = append(dirs, value)
			}
		}
	}
	if common, err := git("rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && common != "" {
		dirs = append(dirs, filepath.Join(common, "hooks"))
	}
	installMu.Lock()
	defer installMu.Unlock()
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(worktree, dir)
		}
		for _, name := range onDemandHookNames {
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				continue
			}
			if err := installHook(hooksDir, name); err != nil {
				return err
			}
		}
	}
	return nil
}

// ConfigureWorktree points one worktree's hooks at hooksDir and sets the
// trailer the hook adds. Everything is written with `git config --worktree`, so
// the person's own checkout and other worktrees are untouched. It needs
// extensions.worktreeConfig, which session worktrees already enable.
// RemoveFromWorktree undoes it.
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
	if out, err := git("config", "--worktree", ConfigCoAuthor, coAuthor); err != nil {
		return fmt.Errorf("attribution: set co-author: %s: %w", out, err)
	}
	return installRepositoryHooks(worktree, hooksDir, git)
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

// ConfiguredFor reports whether the worktree's hooks point at hooksDir, that
// is, whether ConfigureWorktree ran there.
func ConfiguredFor(ctx context.Context, worktree, hooksDir string) bool {
	out, err := aoprocess.CommandContext(ctx, "git", "-C", worktree, "config", "--worktree", "--get", "core.hooksPath").Output()
	return err == nil && filepath.Clean(strings.TrimSpace(string(out))) == filepath.Clean(hooksDir)
}

// configBranchCoAuthor is the per-branch key (branch.<name>.<key>) that keeps a
// session's choice. It lives with the branch rather than the worktree, so it
// survives a reclaimed worktree, and git drops it when the branch is deleted.
const configBranchCoAuthor = "orchestratorCoauthor"

// RecordChoice saves on the worktree's branch the trailer its session started
// with, or that it started without one (empty coAuthor). A detached HEAD has no
// branch to keep it on and is skipped.
func RecordChoice(ctx context.Context, worktree, coAuthor string) error {
	key, ok := branchKey(ctx, worktree)
	if !ok {
		return nil
	}
	args := []string{"-C", worktree, "config", "--local", key, coAuthor}
	if coAuthor == "" {
		args = []string{"-C", worktree, "config", "--local", "--unset-all", key}
	}
	out, err := aoprocess.CommandContext(ctx, "git", args...).CombinedOutput()
	if err != nil && (coAuthor != "" || !isMissingKey(err)) {
		return fmt.Errorf("attribution: record choice: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// RecordedChoice returns the trailer RecordChoice saved for the worktree's
// branch, or "" when its session started without one.
func RecordedChoice(ctx context.Context, worktree string) string {
	key, ok := branchKey(ctx, worktree)
	if !ok {
		return ""
	}
	out, err := aoprocess.CommandContext(ctx, "git", "-C", worktree, "config", "--local", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func branchKey(ctx context.Context, worktree string) (string, bool) {
	out, err := aoprocess.CommandContext(ctx, "git", "-C", worktree, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	branch := strings.TrimSpace(string(out))
	if err != nil || branch == "" {
		return "", false
	}
	return "branch." + branch + "." + configBranchCoAuthor, true
}

// isMissingKey reports git config's exit status 5: the key was not set.
func isMissingKey(err error) bool {
	var exitErr interface{ ExitCode() int }
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 5
}
