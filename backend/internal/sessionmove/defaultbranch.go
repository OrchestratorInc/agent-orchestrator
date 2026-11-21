package sessionmove

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrDefaultBranch refuses a move of a worktree on the repository's default
// branch: the move would push its uncommitted work as a commit onto it.
var ErrDefaultBranch = errors.New("the session is on the default branch")

// RefuseDefaultBranch returns ErrDefaultBranch when the worktree's checked-out
// branch is configured, the project's configured default branch, or origin's
// default branch. It changes nothing. An empty configured branch and AO's
// "auto" placeholder are ignored. A worktree with no branch checked out is
// left to CaptureWorktree to refuse.
//
// It also returns the repository's default branch as the project sees it:
// configured when set, else origin's, or "" when origin names none.
func RefuseDefaultBranch(ctx context.Context, worktree, configured string) (string, error) {
	if configured = strings.TrimSpace(configured); configured == "auto" {
		configured = ""
	}
	originDefault, err := OriginDefaultBranch(ctx, worktree)
	if err != nil {
		return "", err
	}
	def := configured
	if def == "" {
		def = originDefault
	}
	branch, err := gitLine(ctx, worktree, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil || branch == "" {
		return def, nil
	}
	if branch == configured || branch == originDefault {
		return def, fmt.Errorf("%w: %s", ErrDefaultBranch, branch)
	}
	return def, nil
}

// OriginDefaultBranch names origin's default branch: the cached
// refs/remotes/origin/HEAD when the clone has one, else what origin
// advertises. It returns "" when origin names none, as an empty repository
// does.
func OriginDefaultBranch(ctx context.Context, worktree string) (string, error) {
	if ref, err := gitLine(ctx, worktree, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		if branch := strings.TrimPrefix(ref, "refs/remotes/origin/"); branch != ref && branch != "" {
			return branch, nil
		}
	}
	out, err := runGit(ctx, worktree, nil, nil, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "", remoteError(err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		// "ref: refs/heads/main\tHEAD"
		rest, symref := strings.CutPrefix(line, "ref: ")
		target, name, ok := strings.Cut(rest, "\t")
		if symref && ok && name == "HEAD" {
			return strings.TrimPrefix(target, "refs/heads/"), nil
		}
	}
	return "", nil
}
