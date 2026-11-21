package sessionmove

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	// ErrSubmoduleUnpushed: the branch points a submodule at a commit its
	// remote does not have, which the target could not check out.
	ErrSubmoduleUnpushed = errors.New("a submodule commit is not on its remote")
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
	if err := CheckPush(ctx, worktree, c, maxFileBytes); err != nil {
		return err
	}
	tip := c.Tip()
	ref := "refs/heads/" + c.Branch
	if out, err := runGit(ctx, worktree, nil, nil, "push", "--porcelain", "--no-verify", "origin", tip+":"+ref); err != nil {
		err = remoteError(err, out)
		if errors.Is(err, ErrRemoteAhead) {
			return fmt.Errorf("%w: %s", ErrRemoteAhead, c.Branch)
		}
		return err
	}
	remote, err := gitLine(ctx, worktree, "ls-remote", "origin", ref)
	if err != nil {
		return remoteError(err, "")
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

// CheckPush returns the refusal PushBranch would give c before pushing: Git
// LFS files, files over maxFileBytes, or submodule commits not on their
// remote, in commits origin does not have.
func CheckPush(ctx context.Context, worktree string, c Capture, maxFileBytes int64) error {
	big, lfs, err := unpushedBlobs(ctx, worktree, c.Tip(), maxFileBytes)
	if err != nil {
		return err
	}
	if len(lfs) > 0 {
		// ponytail: refuses LFS; upload with `git lfs push origin <tip>` once git-lfs is a dependency.
		return fmt.Errorf("%w: %s", ErrLFSObjects, strings.Join(lfs, ", "))
	}
	if len(big) > 0 {
		return &FileTooLargeError{Limit: maxFileBytes, Files: big}
	}
	subs, err := unpushedSubmodules(ctx, worktree, c.Tip())
	if err != nil {
		return err
	}
	if len(subs) > 0 {
		return fmt.Errorf("%w: %s", ErrSubmoduleUnpushed, strings.Join(subs, ", "))
	}
	return nil
}

// unpushedSubmodules lists the submodules that commits origin lacks point at
// a commit the submodule's remote does not have. A push sends only this
// repository, so the target could not check those commits out.
func unpushedSubmodules(ctx context.Context, worktree, tip string) ([]string, error) {
	// -z keeps paths unquoted: each entry is ":<modes> <ids> <status>" then the path.
	out, err := runGit(ctx, worktree, nil, nil, "log", "--format=", "--raw", "-z", "--no-abbrev", "--no-renames", tip, "--not", "--remotes=origin")
	if err != nil {
		return nil, err
	}
	// Newest first, so the first gitlink seen for a path is the one the tip has.
	pointers := map[string]string{}
	var paths []string
	tokens := strings.Split(out, "\x00")
	for i := 0; i+1 < len(tokens); i++ {
		meta := strings.TrimLeft(tokens[i], "\n")
		if !strings.HasPrefix(meta, ":") {
			continue
		}
		path := tokens[i+1]
		i++
		fields := strings.Fields(meta)
		if len(fields) < 5 || fields[1] != "160000" || fields[4] == "D" {
			continue
		}
		if _, seen := pointers[path]; !seen {
			pointers[path] = fields[3]
			paths = append(paths, path)
		}
	}
	var missing []string
	for _, path := range paths {
		dir := filepath.Join(worktree, path)
		// ponytail: a submodule that is not checked out here cannot be checked; it is let through.
		if _, err := gitLine(ctx, dir, "rev-parse", "--git-dir"); err != nil {
			continue
		}
		published, err := onRemote(ctx, dir, pointers[path])
		if err != nil {
			return nil, err
		}
		if !published {
			missing = append(missing, path)
		}
	}
	return missing, nil
}

// onRemote reports whether a remote-tracking ref of the repository in dir
// contains commit, fetching once before saying no.
func onRemote(ctx context.Context, dir, commit string) (bool, error) {
	contains := func() bool {
		out, err := runGit(ctx, dir, nil, nil, "for-each-ref", "--contains", commit, "--format=%(refname)", "refs/remotes/")
		return err == nil && strings.TrimSpace(out) != ""
	}
	if contains() {
		return true, nil
	}
	if out, err := runGit(ctx, dir, nil, nil, "fetch", "--quiet", "origin"); err != nil {
		return false, remoteError(err, out)
	}
	return contains(), nil
}
