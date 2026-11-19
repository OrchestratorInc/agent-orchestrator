package sessionmove

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Refusals from ImportBranch.
var (
	ErrMissingCommit    = errors.New("origin does not have the moved commit")
	ErrDiverged         = errors.New("the branch here has commits the moved branch does not")
	ErrBranchCheckedOut = errors.New("the branch is checked out in a worktree here")
)

// originHeads fetches every origin branch, whatever refspec the clone has
// (a --single-branch clone fetches only one).
const originHeads = "+refs/heads/*:refs/remotes/origin/*"

// FetchOrigin fetches every origin branch into repo.
func FetchOrigin(ctx context.Context, repo string) error {
	_, err := runGit(ctx, repo, nil, nil, "fetch", "-q", "origin", originHeads)
	return err
}

// ImportBranch fetches origin and makes branch in repo point at tip, which
// AO Cloud pushed. It only creates or fast-forwards the branch.
func ImportBranch(ctx context.Context, repo, branch, tip string) error {
	if err := FetchOrigin(ctx, repo); err != nil {
		return err
	}
	if _, err := runGit(ctx, repo, nil, nil, "cat-file", "-e", tip+"^{commit}"); err != nil {
		return fmt.Errorf("%w: %s", ErrMissingCommit, tip)
	}
	return PlaceBranch(ctx, repo, branch, tip)
}

// ErrLocalAhead reports a branch here with commits origin's lacks, which a
// move from AO Cloud could not fast-forward.
var ErrLocalAhead = errors.New("the branch here has commits origin's does not")

// CheckImportBranch says whether a move from AO Cloud could place branch in
// repo, changing nothing but origin's refs: it refuses a branch checked out in
// a worktree and one with commits origin's branch lacks.
func CheckImportBranch(ctx context.Context, repo, branch string) error {
	if err := FetchOrigin(ctx, repo); err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	if _, err := runGit(ctx, repo, nil, nil, "check-ref-format", ref); err != nil {
		return fmt.Errorf("branch %q: %w", branch, err)
	}
	list, err := runGit(ctx, repo, nil, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(list, "\n") {
		if line == "branch "+ref {
			return fmt.Errorf("%w: %s", ErrBranchCheckedOut, branch)
		}
	}
	local, ok := resolveRef(ctx, repo, ref)
	if !ok {
		return nil
	}
	// Without the branch on origin yet the export pushes it, and the import
	// then refuses a branch here it cannot fast-forward.
	remote, ok := resolveRef(ctx, repo, "refs/remotes/origin/"+branch)
	if !ok {
		return nil
	}
	ahead, err := gitLine(ctx, repo, "rev-list", "--count", remote+".."+local)
	if err != nil {
		return err
	}
	if ahead != "0" {
		return fmt.Errorf("%w: %s", ErrLocalAhead, branch)
	}
	return nil
}

// resolveRef returns the commit ref names, and false when it does not exist.
func resolveRef(ctx context.Context, repo, ref string) (string, bool) {
	id, err := gitLine(ctx, repo, "rev-parse", "-q", "--verify", ref)
	return id, err == nil
}

// PlaceBranch creates branch at tip, or fast-forwards it. It refuses a branch
// with commits tip lacks, and one checked out in a worktree, whose files would
// no longer match it.
func PlaceBranch(ctx context.Context, repo, branch, tip string) error {
	ref := "refs/heads/" + branch
	if _, err := runGit(ctx, repo, nil, nil, "check-ref-format", ref); err != nil {
		return fmt.Errorf("branch %q: %w", branch, err)
	}
	list, err := runGit(ctx, repo, nil, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(list, "\n") {
		if line == "branch "+ref {
			return fmt.Errorf("%w: %s", ErrBranchCheckedOut, branch)
		}
	}
	cur, err := gitLine(ctx, repo, "rev-parse", "-q", "--verify", ref)
	if err != nil {
		_, err = runGit(ctx, repo, nil, nil, "update-ref", ref, tip, "")
		return err
	}
	if cur == tip {
		return nil
	}
	if _, err := runGit(ctx, repo, nil, nil, "merge-base", "--is-ancestor", cur, tip); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return fmt.Errorf("%w: %s", ErrDiverged, branch)
		}
		return err
	}
	_, err = runGit(ctx, repo, nil, nil, "update-ref", ref, tip, cur)
	return err
}
