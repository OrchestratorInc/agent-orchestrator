package sessionmove

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// GitHubFileLimit is the largest file GitHub accepts in a push.
const GitHubFileLimit = 100 << 20

// Refusals from PushBranch.
var (
	ErrRemoteAhead    = errors.New("origin's branch has commits this machine does not")
	ErrFileTooLarge   = errors.New("a file is over the push limit")
	ErrLFSObjects     = errors.New("the move includes Git LFS files")
	ErrPushNotVisible = errors.New("origin does not show the pushed commit")
	ErrBranchMoved    = errors.New("the local branch moved after the capture")
)

// FileTooLargeError lists the files over the push limit.
type FileTooLargeError struct {
	Limit int64
	Files []Blob
}

func (e *FileTooLargeError) Error() string {
	return fmt.Sprintf("%v (%d bytes): %v", ErrFileTooLarge, e.Limit, e.Files)
}

func (e *FileTooLargeError) Unwrap() error { return ErrFileTooLarge }

// PushBranch publishes c to origin's branch for AO Cloud, which continues
// from origin/<branch>. Only after origin shows the commit does it move the
// local branch to it, so a rolled-back move leaves a branch that matches
// origin. It never force-pushes.
func PushBranch(ctx context.Context, worktree string, c Capture, maxFileBytes int64) error {
	if len(c.LFS) > 0 {
		// ponytail: refuses LFS; upload with `git lfs push origin <tip>` once git-lfs is a dependency.
		return fmt.Errorf("%w: %s", ErrLFSObjects, strings.Join(c.LFS, ", "))
	}
	tip := c.Tip()
	big, err := blobsOver(ctx, worktree, tip, maxFileBytes)
	if err != nil {
		return err
	}
	if len(big) > 0 {
		return &FileTooLargeError{Limit: maxFileBytes, Files: big}
	}
	ref := "refs/heads/" + c.Branch
	if out, err := runGit(ctx, worktree, nil, nil, "push", "--porcelain", "--no-verify", "origin", tip+":"+ref); err != nil {
		var ge *gitError
		if errors.As(err, &ge) && (strings.Contains(out, "[rejected]") || strings.Contains(ge.stderr, "[rejected]")) {
			return fmt.Errorf("%w: %s", ErrRemoteAhead, c.Branch)
		}
		return err
	}
	remote, err := gitLine(ctx, worktree, "ls-remote", "origin", ref)
	if err != nil {
		return err
	}
	if id, _, _ := strings.Cut(remote, "\t"); id != tip {
		return fmt.Errorf("%w: origin has %q, want %s", ErrPushNotVisible, id, tip)
	}
	if c.Commit == "" {
		return nil
	}
	if _, err := runGit(ctx, worktree, nil, nil, "update-ref", "-m", "ao move: WIP commit", ref, c.Commit, c.Base); err != nil {
		return fmt.Errorf("%w: %w", ErrBranchMoved, err)
	}
	// The worktree already holds the WIP content; point the index at it too.
	_, err = runGit(ctx, worktree, nil, nil, "reset", "-q")
	return err
}
