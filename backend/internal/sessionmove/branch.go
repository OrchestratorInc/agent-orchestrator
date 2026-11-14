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
