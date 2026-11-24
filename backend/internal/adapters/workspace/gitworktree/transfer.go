package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.WorkspaceTransfer = (*Workspace)(nil)

// indexSuffix marks the ref that holds what was staged, beside the preserved ref.
const indexSuffix = "-index"

// unfinishedGit maps git's per-worktree state files to the operation in progress.
var unfinishedGit = []struct{ file, name string }{
	{"rebase-merge", "a rebase"}, {"rebase-apply", "a rebase"}, {"MERGE_HEAD", "a merge"},
	{"CHERRY_PICK_HEAD", "a cherry-pick"}, {"REVERT_HEAD", "a revert"}, {"BISECT_LOG", "a bisect"},
}

// checkMovable refuses work that a commit and the preserved tree cannot carry.
func (w *Workspace) checkMovable(ctx context.Context, path string) error {
	out, err := w.run(ctx, w.binary, "-C", path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	gitDir := strings.TrimSpace(string(out))
	for _, each := range unfinishedGit {
		if _, err := os.Stat(filepath.Join(gitDir, each.file)); err == nil {
			return fmt.Errorf("%w: %s", ports.ErrTransferGitBusy, each.name)
		}
	}
	unmerged, err := w.run(ctx, w.binary, "-C", path, "ls-files", "--unmerged", "-z")
	if err != nil {
		return err
	}
	if len(unmerged) > 0 {
		return fmt.Errorf("%w: unresolved conflicts", ports.ErrTransferGitBusy)
	}
	status, err := w.run(ctx, w.binary, "-C", path, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(string(status), "\x00") {
		// git lists untracked files one by one; a name ending in / is a repository it will not enter.
		if dir, ok := strings.CutPrefix(entry, "?? "); ok && strings.HasSuffix(dir, "/") {
			return fmt.Errorf("%w: %s", ports.ErrTransferNestedRepo, strings.TrimSuffix(dir, "/"))
		}
	}
	return nil
}

// stagedCommit records the index as a commit when it differs from HEAD, or "".
func (w *Workspace) stagedCommit(ctx context.Context, path, head string) (string, error) {
	tree, err := w.run(ctx, w.binary, "-C", path, "write-tree")
	if err != nil {
		return "", err
	}
	if headTree, err := w.revParse(ctx, path, head+"^{tree}"); err != nil || headTree == strings.TrimSpace(string(tree)) {
		return "", err
	}
	out, err := w.run(ctx, w.binary, "-C", path, "-c", "user.name=Orchestrator", "-c", "user.email=orchestrator@localhost",
		"commit-tree", strings.TrimSpace(string(tree)), "-p", head, "-m", "staged changes")
	return strings.TrimSpace(string(out)), err
}

var gitCredentials = regexp.MustCompile(`://[^/\s@]+@`)

// gitSaid is the end of a git command's output, without credentials in URLs.
func gitSaid(out []byte) string {
	lines := strings.Split(strings.TrimSpace(gitCredentials.ReplaceAllString(string(out), "://")), "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	text := strings.Join(lines, " ")
	if len(text) > 400 {
		text = text[:400]
	}
	return text
}

// CheckTransfer refuses a worktree whose work a push cannot carry.
func (w *Workspace) CheckTransfer(ctx context.Context, info ports.WorkspaceInfo) error {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return err
	}
	if err := w.checkMovable(ctx, path); err != nil {
		return err
	}
	out, err := w.run(ctx, w.binary, "-C", path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(string(out)) != info.Branch {
		return fmt.Errorf("%w: %s", ports.ErrDetachedHead, info.Branch)
	}
	return nil
}

// PushTransfer pushes the worktree's branch, and the preserved ref when set,
// to origin without forcing. It returns the branch head.
func (w *Workspace) PushTransfer(ctx context.Context, info ports.WorkspaceInfo, preservedRef, remotePreservedRef string) (string, error) {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return "", err
	}
	if err := w.CheckTransfer(ctx, info); err != nil {
		return "", err
	}
	head, err := w.revParse(ctx, path, "HEAD")
	if err != nil {
		return "", err
	}
	refspecs := []string{"HEAD:refs/heads/" + info.Branch}
	if preservedRef != "" {
		if !strings.HasPrefix(remotePreservedRef, "refs/ao/preserved/") {
			return "", fmt.Errorf("gitworktree: invalid preserved ref %q", remotePreservedRef)
		}
		refspecs = append(refspecs, preservedRef+":"+remotePreservedRef)
		staged, err := w.stagedCommit(ctx, path, head)
		if err != nil {
			return "", err
		}
		if staged != "" {
			refspecs = append(refspecs, staged+":"+remotePreservedRef+indexSuffix)
		}
	}
	args := append([]string{"-C", path, "push", "--porcelain", "origin"}, refspecs...)
	if out, err := w.run(ctx, w.binary, args...); err != nil {
		return "", fmt.Errorf("%w: %s", ports.ErrTransferPush, gitSaid(out))
	}
	return head, nil
}

// FetchTransfer fetches a moved branch and, when set, its preserved ref from origin.
func (w *Workspace) FetchTransfer(ctx context.Context, repoPath, branch, remotePreservedRef string) error {
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	if err := w.validateBranch(ctx, repo, branch); err != nil {
		return err
	}
	refspecs := []string{"+refs/heads/" + branch + ":refs/remotes/origin/" + branch}
	if remotePreservedRef != "" {
		if !strings.HasPrefix(remotePreservedRef, "refs/ao/preserved/") {
			return fmt.Errorf("gitworktree: invalid preserved ref %q", remotePreservedRef)
		}
		// The trailing * also takes the staged state's ref, which is not always there.
		refspecs = append(refspecs, "+"+remotePreservedRef+"*:"+remotePreservedRef+"*")
	}
	args := append([]string{"-C", repo, "fetch", "origin"}, refspecs...)
	if out, err := w.run(ctx, w.binary, args...); err != nil {
		return fmt.Errorf("%w: %s", ports.ErrTransferFetch, gitSaid(out))
	}
	return nil
}

// AlignTransferBranch points the local branch at head when it is missing or
// behind head. A local branch with commits head lacks is left alone and
// returns ErrTransferBranchDiverged.
func (w *Workspace) AlignTransferBranch(ctx context.Context, repoPath, branch, head string) error {
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	if err := w.validateBranch(ctx, repo, branch); err != nil {
		return err
	}
	local := "refs/heads/" + branch
	exists, err := w.refExists(ctx, repo, local)
	if err != nil {
		return err
	}
	if !exists {
		_, err := w.run(ctx, w.binary, "-C", repo, "branch", "--no-track", branch, head)
		return err
	}
	current, err := w.revParse(ctx, repo, local)
	if err != nil || current == head {
		return err
	}
	if _, err := w.run(ctx, w.binary, "-C", repo, "merge-base", "--is-ancestor", current, head); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return fmt.Errorf("%w: %s", ports.ErrTransferBranchDiverged, branch)
		}
		return err
	}
	// Refuses a branch checked out in a worktree, which a spawn could not use anyway.
	_, err = w.run(ctx, w.binary, "-C", repo, "branch", "--force", "--no-track", branch, head)
	return err
}

// RevParse resolves a ref in the repository.
func (w *Workspace) RevParse(ctx context.Context, repoPath, ref string) (string, error) {
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return "", fmt.Errorf("gitworktree: repo path: %w", err)
	}
	return w.revParse(ctx, repo, ref)
}

// ReleaseTransferBranch detaches HEAD in the worktree, keeping its files.
func (w *Workspace) ReleaseTransferBranch(ctx context.Context, info ports.WorkspaceInfo) error {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return err
	}
	if out, err := w.run(ctx, w.binary, "-C", path, "switch", "--detach"); err != nil {
		return fmt.Errorf("gitworktree: detach %s: %w: %s", info.Branch, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RestoreTransferIndex puts the index back as it was on the source, which
// ApplyPreserved leaves fully staged: to the staged state that came along, or
// to nothing staged.
func (w *Workspace) RestoreTransferIndex(ctx context.Context, info ports.WorkspaceInfo, remotePreservedRef string) error {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return err
	}
	staged := remotePreservedRef + indexSuffix
	exists, err := w.refExists(ctx, path, staged)
	if err != nil {
		return err
	}
	if !exists {
		if out, err := w.run(ctx, w.binary, "-C", path, "reset", "-q"); err != nil {
			return fmt.Errorf("gitworktree: unstage moved work: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	defer func() { _, _ = w.run(ctx, w.binary, "-C", path, "update-ref", "-d", staged) }()
	if out, err := w.run(ctx, w.binary, "-C", path, "read-tree", staged); err != nil {
		return fmt.Errorf("gitworktree: restore staged work: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// Refresh the cached file stats; files that differ from the index report as such.
	_, _ = w.run(ctx, w.binary, "-C", path, "update-index", "-q", "--refresh")
	return nil
}

// DeleteTransferRef deletes a preserved ref, and the staged state beside it,
// from origin. A ref that is already gone is not an error.
func (w *Workspace) DeleteTransferRef(ctx context.Context, repoPath, remotePreservedRef string) error {
	if !strings.HasPrefix(remotePreservedRef, "refs/ao/preserved/") {
		return fmt.Errorf("gitworktree: invalid preserved ref %q", remotePreservedRef)
	}
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	out, err := w.run(ctx, w.binary, "-C", repo, "ls-remote", "origin", remotePreservedRef, remotePreservedRef+indexSuffix)
	if err != nil {
		return fmt.Errorf("gitworktree: ls-remote: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var refspecs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if _, ref, ok := strings.Cut(line, "\t"); ok {
			refspecs = append(refspecs, ":"+ref)
		}
	}
	if len(refspecs) == 0 {
		return nil
	}
	args := append([]string{"-C", repo, "push", "--porcelain", "origin"}, refspecs...)
	if out, err := w.run(ctx, w.binary, args...); err != nil {
		return fmt.Errorf("gitworktree: delete %s: %w: %s", remotePreservedRef, err, strings.TrimSpace(string(out)))
	}
	return nil
}
