package sessionmove

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Refusals a caller reports before anything moves.
var (
	ErrOperationInProgress = errors.New("a git operation is in progress")
	ErrDetachedHead        = errors.New("no branch is checked out")
	ErrNoCommits           = errors.New("the branch has no commits")
	ErrDirtySubmodule      = errors.New("a submodule has uncommitted changes")
)

const lfsPointerPrefix = "version https://git-lfs.github.com/spec/v1"

// CaptureOptions selects the worktree to capture.
type CaptureOptions struct {
	Worktree string
	// Label names the source machine in the WIP commit message.
	Label string
	// Exclude lists repo-relative paths that stay behind as they are in HEAD,
	// such as AO project symlinks or files the user chose not to move.
	Exclude []string
}

// Capture is a session's code state: Base plus, when the worktree has
// uncommitted work, one WIP commit on top of it.
type Capture struct {
	Branch string
	Base   string
	// Commit is the WIP commit, or "" when the worktree matches Base.
	Commit  string
	Changes []Change
	// LFS lists changed paths whose content is a Git LFS pointer. The objects
	// they point at may exist only in this machine's LFS store.
	LFS []string
	// Submodules lists submodules checked out at a commit other than Base's.
	// The target can check them out only if that commit is on their remote.
	Submodules []string
}

// Tip is the commit the target's branch ends at.
func (c Capture) Tip() string {
	if c.Commit != "" {
		return c.Commit
	}
	return c.Base
}

// Change is one path the WIP commit changes relative to Base.
type Change struct {
	Path string
	// Status is git's raw diff status: 'A', 'M', 'D' or 'T' (type change).
	Status byte
	// Size is the captured blob's size; 0 for deletions and submodules.
	Size int64
}

// CaptureWorktree records the worktree's uncommitted work, tracked and
// untracked but not ignored, as a commit on top of HEAD. The worktree, its
// index and its refs are left exactly as they were.
func CaptureWorktree(ctx context.Context, opts CaptureOptions) (Capture, error) {
	wt := opts.Worktree
	if _, err := gitLine(ctx, wt, "rev-parse", "--show-toplevel"); err != nil {
		return Capture{}, fmt.Errorf("not a git worktree: %w", err)
	}
	if err := refuseOperation(ctx, wt); err != nil {
		return Capture{}, err
	}
	branch, err := gitLine(ctx, wt, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return Capture{}, ErrDetachedHead
	}
	base, err := gitLine(ctx, wt, "rev-parse", "-q", "--verify", "HEAD^{commit}")
	if err != nil {
		return Capture{}, ErrNoCommits
	}
	c := Capture{Branch: branch, Base: base}
	if c.Submodules, err = checkStatus(ctx, wt); err != nil {
		return Capture{}, err
	}

	index, cleanup, err := copyIndex(ctx, wt)
	if err != nil {
		return Capture{}, err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + index}
	addArgs := []string{"-c", "core.splitIndex=false", "add", "-A"}
	if len(opts.Exclude) > 0 {
		addArgs = append(addArgs, "--")
		for _, p := range opts.Exclude {
			addArgs = append(addArgs, ":(exclude,literal)"+p)
		}
	}
	if _, err := runGit(ctx, wt, env, nil, addArgs...); err != nil {
		return Capture{}, err
	}
	if err := resetPaths(ctx, wt, env, base, opts.Exclude); err != nil {
		return Capture{}, err
	}
	tree, err := runGit(ctx, wt, env, nil, "write-tree")
	if err != nil {
		return Capture{}, err
	}
	tree = strings.TrimSpace(tree)
	baseTree, err := gitLine(ctx, wt, "rev-parse", base+"^{tree}")
	if err != nil {
		return Capture{}, err
	}
	if tree == baseTree {
		return c, nil
	}

	label := opts.Label
	if label == "" {
		label = "the source machine"
	}
	// The base's date makes the commit repeatable: a retry after a push that
	// origin took but this side could not confirm pushes the same commit again.
	date, err := gitLine(ctx, wt, "show", "-s", "--format=%cd", "--date=raw", base)
	if err != nil {
		return Capture{}, err
	}
	commitEnv := append(identityEnv(ctx, wt), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	commit, err := runGit(ctx, wt, commitEnv, nil, "commit-tree", "-p", base,
		"-m", "WIP: uncommitted changes from "+label, tree)
	if err != nil {
		return Capture{}, err
	}
	c.Commit = strings.TrimSpace(commit)
	if c.Changes, c.LFS, err = changes(ctx, wt, base, c.Commit); err != nil {
		return Capture{}, err
	}
	return c, nil
}

// refuseOperation stops a capture in the middle of a merge, rebase,
// cherry-pick or revert: the capture would keep one parent and drop that state.
func refuseOperation(ctx context.Context, wt string) error {
	names := []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply"}
	args := make([]string, 0, 1+2*len(names))
	args = append(args, "rev-parse")
	for _, n := range names {
		args = append(args, "--git-path", n)
	}
	out, err := runGit(ctx, wt, nil, nil, args...)
	if err != nil {
		return err
	}
	for i, p := range strings.Split(strings.TrimSpace(out), "\n") {
		if !filepath.IsAbs(p) {
			p = filepath.Join(wt, p)
		}
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%w: %s exists", ErrOperationInProgress, names[i])
		}
	}
	return nil
}

// checkStatus refuses unresolved conflicts and dirty submodules, and returns
// submodules whose checked-out commit moved.
func checkStatus(ctx context.Context, wt string) ([]string, error) {
	out, err := runGit(ctx, wt, nil, nil, "status", "--porcelain=v2", "-z", "--untracked-files=no", "--ignore-submodules=none")
	if err != nil {
		return nil, err
	}
	var moved []string
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if f == "" {
			continue
		}
		parts := strings.SplitN(f, " ", 9)
		switch f[0] {
		case 'u':
			return nil, fmt.Errorf("%w: unresolved conflict in %s", ErrOperationInProgress, lastField(f, 11))
		case '2':
			i++ // a rename record is followed by its original path
			parts = strings.SplitN(f, " ", 10)
		case '1':
		default:
			continue
		}
		if len(parts) < 9 || !strings.HasPrefix(parts[2], "S") || len(parts[2]) != 4 {
			continue
		}
		sub, path := parts[2], parts[len(parts)-1]
		if sub[2] == 'M' || sub[3] == 'U' {
			return nil, fmt.Errorf("%w: %s", ErrDirtySubmodule, path)
		}
		if sub[1] == 'C' {
			moved = append(moved, path)
		}
	}
	return moved, nil
}

func lastField(record string, n int) string {
	parts := strings.SplitN(record, " ", n)
	return parts[len(parts)-1]
}

// copyIndex copies the real index to a temp file. Starting from it keeps the
// stat cache (only changed files are re-read) and the skip-worktree bits, so a
// sparse checkout's absent files are not captured as deletions.
func copyIndex(ctx context.Context, wt string) (string, func(), error) {
	indexPath, err := gitLine(ctx, wt, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", nil, err
	}
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(wt, indexPath)
	}
	tmp, err := os.CreateTemp("", "ao-move-index-*")
	if err != nil {
		return "", nil, err
	}
	path := tmp.Name()
	cleanup := func() { _ = os.Remove(path) }
	data, err := os.ReadFile(indexPath)
	if err == nil {
		_, err = tmp.Write(data)
	} else if errors.Is(err, os.ErrNotExist) {
		err = os.Remove(path) // git creates a missing index
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

// resetPaths puts excluded paths back to base in the temp index, which also
// drops anything the user had staged for them.
func resetPaths(ctx context.Context, wt string, env []string, base string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	rm := []string{"rm", "-r", "--cached", "-q", "--ignore-unmatch", "--"}
	for _, p := range paths {
		rm = append(rm, ":(literal)"+p)
	}
	if _, err := runGit(ctx, wt, env, nil, rm...); err != nil {
		return err
	}
	entries, err := runGit(ctx, wt, nil, nil, append([]string{"ls-tree", "-r", "-z", "--full-tree", base, "--"}, paths...)...)
	if err != nil || entries == "" {
		return err
	}
	_, err = runGit(ctx, wt, env, strings.NewReader(entries), "update-index", "-z", "--index-info")
	return err
}

// identityEnv falls back to an AO identity when the repo has none, so a
// machine without user.name and user.email can still move its work.
func identityEnv(ctx context.Context, wt string) []string {
	if _, err := runGit(ctx, wt, nil, nil, "var", "GIT_COMMITTER_IDENT"); err == nil {
		if _, err := runGit(ctx, wt, nil, nil, "var", "GIT_AUTHOR_IDENT"); err == nil {
			return nil
		}
	}
	return []string{
		"GIT_AUTHOR_NAME=AO", "GIT_AUTHOR_EMAIL=ao@localhost",
		"GIT_COMMITTER_NAME=AO", "GIT_COMMITTER_EMAIL=ao@localhost",
	}
}

// changes lists what commit changes relative to base, with blob sizes, and
// the changed paths whose content is an LFS pointer.
func changes(ctx context.Context, wt, base, commit string) ([]Change, []string, error) {
	out, err := runGit(ctx, wt, nil, nil, "diff-tree", "-r", "-z", "--no-renames", "--raw", base, commit)
	if err != nil {
		return nil, nil, err
	}
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	var list []Change
	var ids []string
	var blobIdx []int
	for i := 0; i+1 < len(fields); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		if len(meta) != 5 {
			return nil, nil, fmt.Errorf("diff-tree record %q", fields[i])
		}
		ch := Change{Path: fields[i+1], Status: meta[4][0]}
		if ch.Status != 'D' && meta[1] != "160000" {
			ids = append(ids, meta[3])
			blobIdx = append(blobIdx, len(list))
		}
		list = append(list, ch)
	}
	sizes, err := objectSizes(ctx, wt, ids)
	if err != nil {
		return nil, nil, err
	}
	var small []string
	for i, idx := range blobIdx {
		list[idx].Size = sizes[i]
		if sizes[i] > 0 && sizes[i] < 1024 {
			small = append(small, ids[i])
		}
	}
	contents, err := blobContents(ctx, wt, small)
	if err != nil {
		return nil, nil, err
	}
	var lfs []string
	for i, idx := range blobIdx {
		if bytes.HasPrefix(contents[ids[i]], []byte(lfsPointerPrefix)) {
			lfs = append(lfs, list[idx].Path)
		}
	}
	return list, lfs, nil
}
