package forkseed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const RefPrefix = "refs/ao/fork-seeds/"

var ErrNotRepository = errors.New("forkseed: not a git repository")

func Capture(ctx context.Context, workspace, ref string) (string, error) {
	if _, err := run(ctx, workspace, nil, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", ErrNotRepository
	}
	head, err := run(ctx, workspace, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("forkseed: read HEAD: %w", err)
	}
	if ref == "" {
		return head, nil
	}
	if !strings.HasPrefix(ref, RefPrefix) {
		return "", fmt.Errorf("forkseed: ref %q is outside %s", ref, RefPrefix)
	}
	return head, record(ctx, workspace, head, ref)
}

func record(ctx context.Context, workspace, head, ref string) error {
	tree, err := worktreeTree(ctx, workspace)
	if err != nil {
		return err
	}
	commit, err := run(ctx, workspace, identityEnv, "commit-tree", tree, "-p", head, "-m", "AO fork seed")
	if err != nil {
		return fmt.Errorf("forkseed: commit files: %w", err)
	}
	if _, err := run(ctx, workspace, nil, "update-ref", ref, commit); err != nil {
		return fmt.Errorf("forkseed: record ref: %w", err)
	}
	return nil
}

func worktreeTree(ctx context.Context, workspace string) (string, error) {
	index, err := tempIndex(ctx, workspace)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(index) }()
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := run(ctx, workspace, env, "add", "-A", "--", "."); err != nil {
		return "", fmt.Errorf("forkseed: stage files: %w", err)
	}
	tree, err := run(ctx, workspace, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("forkseed: write tree: %w", err)
	}
	return tree, nil
}

const BasePrefix = "refs/ao/fork-base/"

func BaseRef(forkSessionID string) string { return BasePrefix + forkSessionID }

func RecordBase(ctx context.Context, worktree, ref string) error {
	if !strings.HasPrefix(ref, BasePrefix) {
		return fmt.Errorf("forkseed: ref %q is outside %s", ref, BasePrefix)
	}
	head, err := run(ctx, worktree, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return fmt.Errorf("forkseed: read HEAD: %w", err)
	}
	return record(ctx, worktree, head, ref)
}

func UnchangedSince(ctx context.Context, worktree, ref string) (bool, error) {
	base, err := run(ctx, worktree, nil, "rev-parse", "--verify", "--quiet", ref+"^{tree}")
	if err != nil {
		return false, nil //nolint:nilerr // a missing ref reports false, as documented
	}
	now, err := worktreeTree(ctx, worktree)
	if err != nil {
		return false, err
	}
	return now == base, nil
}

func RestoreBase(ctx context.Context, worktree, ref string) (applied bool, err error) {
	if !strings.HasPrefix(ref, BasePrefix) {
		return false, fmt.Errorf("forkseed: ref %q is outside %s", ref, BasePrefix)
	}
	start, err := run(ctx, worktree, nil, "rev-parse", "--verify", "--quiet", ref+"^1")
	if err != nil {
		return false, nil //nolint:nilerr // no recorded start: nothing to restore
	}
	head, err := run(ctx, worktree, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return false, fmt.Errorf("forkseed: read HEAD: %w", err)
	}
	if head != start {
		return false, nil
	}
	status, err := run(ctx, worktree, nil, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, fmt.Errorf("forkseed: read status: %w", err)
	}
	if status != "" {
		return false, nil
	}
	if err := Apply(ctx, worktree, ref); err != nil {
		return false, err
	}
	return true, nil
}

func DiscardChanges(ctx context.Context, worktree string) error {
	if _, err := run(ctx, worktree, nil, "reset", "-q", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("forkseed: reset: %w", err)
	}
	if _, err := run(ctx, worktree, nil, "clean", "-fdq"); err != nil {
		return fmt.Errorf("forkseed: clean: %w", err)
	}
	return nil
}

func Apply(ctx context.Context, worktree, ref string) error {
	if _, err := run(ctx, worktree, nil, "checkout", ref, "--", "."); err != nil {
		return fmt.Errorf("forkseed: write files: %w", err)
	}
	deleted, err := run(ctx, worktree, nil, "diff", "--name-only", "-z", "--no-renames", "--diff-filter=D", "HEAD", ref)
	if err != nil {
		return fmt.Errorf("forkseed: list deleted files: %w", err)
	}
	for _, name := range strings.Split(deleted, "\x00") {
		if name == "" {
			continue
		}
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(name))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("forkseed: delete %s: %w", name, err)
		}
	}
	if _, err := run(ctx, worktree, nil, "reset", "-q"); err != nil {
		return fmt.Errorf("forkseed: unstage files: %w", err)
	}
	return nil
}

var ErrSnapshotMissing = errors.New("forkseed: snapshot missing")

func SnapshotHead(ctx context.Context, workspace, ref string) (string, error) {
	if _, err := run(ctx, workspace, nil, "rev-parse", "--verify", "--quiet", ref); err != nil {
		return "", ErrSnapshotMissing
	}
	head, err := run(ctx, workspace, nil, "rev-parse", "--verify", ref+"^1")
	if err != nil {
		return "", fmt.Errorf("forkseed: read snapshot base: %w", err)
	}
	return head, nil
}

func Delete(ctx context.Context, workspace, ref string) error {
	if ref == "" {
		return nil
	}
	if !strings.HasPrefix(ref, RefPrefix) && !strings.HasPrefix(ref, BasePrefix) {
		return fmt.Errorf("forkseed: refusing to delete %q outside %s", ref, RefPrefix)
	}
	if _, err := run(ctx, workspace, nil, "rev-parse", "--verify", "--quiet", ref); err != nil {
		return nil //nolint:nilerr // already gone: nothing to delete
	}
	if _, err := run(ctx, workspace, nil, "update-ref", "-d", ref); err != nil {
		return fmt.Errorf("forkseed: delete ref: %w", err)
	}
	return nil
}

func List(ctx context.Context, workspace string) ([]string, error) {
	out, err := run(ctx, workspace, nil, "for-each-ref", "--format=%(refname)", RefPrefix)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			refs = append(refs, line)
		}
	}
	return refs, nil
}

func tempIndex(ctx context.Context, workspace string) (string, error) {
	indexPath, err := run(ctx, workspace, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", fmt.Errorf("forkseed: find index: %w", err)
	}
	tmp, err := os.CreateTemp("", "ao-fork-index-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = tmp.Close() }()
	src, err := os.Open(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return tmp.Name(), nil
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	defer func() { _ = src.Close() }()
	if _, err := io.Copy(tmp, src); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

var identityEnv = []string{
	"GIT_AUTHOR_NAME=AO", "GIT_AUTHOR_EMAIL=ao@localhost",
	"GIT_COMMITTER_NAME=AO", "GIT_COMMITTER_EMAIL=ao@localhost",
}

func run(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func Reap(ctx context.Context, workspace string, maxAge time.Duration, now time.Time) (int, error) {
	out, err := run(ctx, workspace, nil, "for-each-ref", "--format=%(refname) %(committerdate:unix)", RefPrefix)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, line := range strings.Split(out, "\n") {
		ref, stamp, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		seconds, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil || now.Sub(time.Unix(seconds, 0)) < maxAge {
			continue
		}
		if err := Delete(ctx, workspace, ref); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
