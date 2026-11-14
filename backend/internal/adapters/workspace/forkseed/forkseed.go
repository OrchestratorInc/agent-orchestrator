// Package forkseed copies a worktree's current files, including uncommitted
// and untracked ones, into a new worktree for a session fork.
//
// Capture records the files as a commit under a ref, using a temporary index
// so the source's branch, index, and stash are never touched. Apply writes
// that commit's files into the fork's worktree and leaves them uncommitted.
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

// RefPrefix holds every seed ref. Leftovers here with no fork are reaped.
const RefPrefix = "refs/ao/fork-seeds/"

// ErrNotRepository means the workspace isn't in a git repository.
var ErrNotRepository = errors.New("forkseed: not a git repository")

// Capture returns the workspace's HEAD and, when ref is set, records the
// workspace's files under ref. Ignored files are left out.
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

// record commits the workspace's files, on top of head, under ref.
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

// worktreeTree is the tree of the workspace's files as they are now, ignored
// files left out, written without touching the real index.
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

// BasePrefix holds each fork's starting files, so cleanup can tell inherited
// uncommitted changes from the fork's own.
const BasePrefix = "refs/ao/fork-base/"

// BaseRef names a fork's starting files.
func BaseRef(forkSessionID string) string { return BasePrefix + forkSessionID }

// RecordBase records the fork worktree's files, as it starts, under ref.
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

// UnchangedSince reports whether the worktree's files are exactly those
// recorded under ref. A missing ref reports false.
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

// RestoreBase writes a fork's starting files, recorded under ref, back into
// its worktree, uncommitted, when the worktree was recreated and lost them.
// It does so only while the worktree is still at the commit the fork started
// on: a fork that committed since has its own history, which the starting
// files would undo. A worktree that already has changes, such as the fork's
// own edits replayed by restore, is left alone. applied is false when nothing
// was written.
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
	// Restore may already have put the fork's own edits back, from the copy
	// archiving preserved; the starting files would overwrite them.
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

// DiscardChanges drops the worktree's uncommitted changes and untracked
// files, keeping ignored ones such as dependencies.
func DiscardChanges(ctx context.Context, worktree string) error {
	if _, err := run(ctx, worktree, nil, "reset", "-q", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("forkseed: reset: %w", err)
	}
	if _, err := run(ctx, worktree, nil, "clean", "-fdq"); err != nil {
		return fmt.Errorf("forkseed: clean: %w", err)
	}
	return nil
}

// Apply writes the seed's files into worktree, which must be checked out at
// the seed's parent. The files stay uncommitted.
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
	// Unstage, so the fork shows the same changes as uncommitted work.
	if _, err := run(ctx, worktree, nil, "reset", "-q"); err != nil {
		return fmt.Errorf("forkseed: unstage files: %w", err)
	}
	return nil
}

// ErrSnapshotMissing means no snapshot exists under the ref.
var ErrSnapshotMissing = errors.New("forkseed: snapshot missing")

// SnapshotHead is the commit a snapshot was taken on: its first parent. It
// reads seeds and the rollback snapshots saved before each turn alike.
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

// Delete removes the seed ref. A missing ref is not an error. Only refs the
// fork created are deleted; a borrowed snapshot under another prefix is kept.
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

// List returns every seed ref in the workspace's repository.
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

// tempIndex copies the real index so `git add` reuses its file stats.
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

// Reap deletes seed refs created more than maxAge before now. A seed lives
// only while its fork is being created, so an old one is a crash leftover.
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
