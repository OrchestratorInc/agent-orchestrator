package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.WorkspaceTransfer = (*Workspace)(nil)

// PushTransfer pushes the worktree's branch, and the preserved ref when set,
// to origin without forcing. It returns the branch head.
func (w *Workspace) PushTransfer(ctx context.Context, info ports.WorkspaceInfo, preservedRef, remotePreservedRef string) (string, error) {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return "", err
	}
	out, err := w.run(ctx, w.binary, "-C", path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(string(out)) != info.Branch {
		return "", fmt.Errorf("%w: %s", ports.ErrDetachedHead, info.Branch)
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
	}
	args := append([]string{"-C", path, "push", "--porcelain", "origin"}, refspecs...)
	if out, err := w.run(ctx, w.binary, args...); err != nil {
		return "", fmt.Errorf("gitworktree: push %s: %w: %s", info.Branch, err, strings.TrimSpace(string(out)))
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
		refspecs = append(refspecs, "+"+remotePreservedRef+":"+remotePreservedRef)
	}
	args := append([]string{"-C", repo, "fetch", "origin"}, refspecs...)
	if out, err := w.run(ctx, w.binary, args...); err != nil {
		return fmt.Errorf("gitworktree: fetch %s: %w: %s", branch, err, strings.TrimSpace(string(out)))
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

// DeleteTransferRef deletes a preserved ref from origin. A ref that is already
// gone is not an error.
func (w *Workspace) DeleteTransferRef(ctx context.Context, repoPath, remotePreservedRef string) error {
	if !strings.HasPrefix(remotePreservedRef, "refs/ao/preserved/") {
		return fmt.Errorf("gitworktree: invalid preserved ref %q", remotePreservedRef)
	}
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	out, err := w.run(ctx, w.binary, "-C", repo, "ls-remote", "origin", remotePreservedRef)
	if err != nil {
		return fmt.Errorf("gitworktree: ls-remote: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil
	}
	if out, err := w.run(ctx, w.binary, "-C", repo, "push", "--porcelain", "origin", ":"+remotePreservedRef); err != nil {
		return fmt.Errorf("gitworktree: delete %s: %w: %s", remotePreservedRef, err, strings.TrimSpace(string(out)))
	}
	return nil
}
