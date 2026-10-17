package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// NameTaskWorkspace finalizes names before setup or agent processes can use the
// checkout. The caller holds its project workspace gate. Git never force-renames
// branches or overwrites destinations, including when another daemon claims them.
func (w *Workspace) NameTaskWorkspace(ctx context.Context, info ports.WorkspaceProjectInfo, branch, name string) (ports.WorkspaceProjectInfo, error) {
	if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" || strings.HasPrefix(name, "-") || len(name) > 50 {
		return info, fmt.Errorf("gitworktree: invalid task name %q", name)
	}
	oldPath, err := w.validateManagedPath(info.Root.Path)
	if err != nil {
		return info, err
	}
	if err := validatePathComponent("project id", string(info.Root.ProjectID)); err != nil {
		return info, err
	}
	parent := filepath.Join(w.managedRoot, string(info.Root.ProjectID))
	if filepath.Dir(oldPath) != parent {
		return info, fmt.Errorf("gitworktree: task path is outside its project")
	}
	repos := append([]ports.WorkspaceRepoInfo(nil), info.Worktrees...)
	if len(repos) == 0 {
		repo := info.Root.RepoPath
		if repo == "" {
			repo, err = w.repoPath(info.Root.ProjectID)
			if err != nil {
				return info, err
			}
		}
		repos = []ports.WorkspaceRepoInfo{{RepoPath: repo, Path: oldPath, Branch: info.Root.Branch}}
	}
	for _, repo := range repos {
		inside, err := pathWithin(oldPath, repo.Path)
		if err != nil || !inside {
			return info, fmt.Errorf("gitworktree: task repository path is outside its workspace: %s", repo.Path)
		}
	}
	if err := w.validateBranch(ctx, repos[0].RepoPath, branch); err != nil {
		return info, err
	}
	namespace := strings.TrimSuffix(branch, "/root")
	for suffix := 1; suffix <= 100; suffix++ {
		candidate, candidateNamespace := name, namespace
		if suffix > 1 {
			candidate = fmt.Sprintf("%s-%d", name, suffix)
			candidateNamespace = fmt.Sprintf("%s-%d", namespace, suffix)
		}
		path := filepath.Join(parent, candidate)
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return info, err
		}
		result, retry, renameErr := w.nameTaskCandidate(ctx, info, repos, candidateNamespace, path)
		if retry {
			continue
		}
		return result, renameErr
	}
	return info, fmt.Errorf("gitworktree: no unused task name for %q", name)
}

func (w *Workspace) nameTaskCandidate(ctx context.Context, info ports.WorkspaceProjectInfo, repos []ports.WorkspaceRepoInfo, namespace, path string) (ports.WorkspaceProjectInfo, bool, error) {
	branch := namespace + "/root"
	for _, repo := range repos {
		relative, err := filepath.Rel(info.Root.Path, repo.Path)
		if err != nil {
			return info, false, err
		}
		records, err := w.listRecords(ctx, repo.RepoPath)
		if err != nil {
			return info, false, err
		}
		if _, exists := findWorktree(records, filepath.Join(path, relative)); exists {
			return info, true, nil
		}
		out, err := w.run(ctx, w.binary, "-C", repo.RepoPath, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
		if err != nil {
			return info, false, err
		}
		for _, ref := range strings.Fields(string(out)) {
			if remote, ok := strings.CutPrefix(ref, "refs/remotes/"); ok {
				_, ref, _ = strings.Cut(remote, "/")
			} else {
				ref = strings.TrimPrefix(ref, "refs/heads/")
			}
			if ref == namespace || strings.HasPrefix(ref, namespace+"/") || strings.HasPrefix(namespace, ref+"/") {
				return info, true, nil
			}
		}
	}
	oldPath := info.Root.Path
	target := info
	target.Worktrees = append([]ports.WorkspaceRepoInfo(nil), info.Worktrees...)
	target.Root.Path, target.Root.Branch = path, branch
	for i := range target.Worktrees {
		relative, err := filepath.Rel(oldPath, target.Worktrees[i].Path)
		if err != nil {
			return info, false, err
		}
		target.Worktrees[i].Path, target.Worktrees[i].Branch = filepath.Join(path, relative), branch
	}
	info.Root.RepoPath, target.Root.RepoPath = repos[0].RepoPath, repos[0].RepoPath
	move := taskNamingMove{Original: info, Target: target}
	for _, repo := range repos {
		gitDir, err := checkoutGitDir(repo.Path)
		if err != nil {
			return info, false, err
		}
		move.GitDirs = append(move.GitDirs, gitDir)
	}
	journal, err := w.taskNamingJournal(info.Root.ProjectID, info.Root.SessionID)
	if err != nil {
		return info, false, err
	}
	if err := writeTaskNamingMove(ctx, journal, move); err != nil {
		return info, false, err
	}
	// Roll back only before a successful move. After the move, returned paths
	// must describe reality even if a child registration needs repair.
	renamed := 0
	rollbackFailed := false
	rollback := func(cause error) (ports.WorkspaceProjectInfo, bool, error) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		for i := renamed - 1; i >= 0; i-- {
			if _, err := w.run(cleanupCtx, w.binary, "-C", repos[i].Path, "branch", "-m", repos[i].Branch); err != nil {
				rollbackFailed = true
				cause = errors.Join(cause, err)
				if len(info.Worktrees) > i {
					info.Worktrees[i].Branch = branch
				}
				if i == 0 {
					info.Root.Branch = branch
				}
			}
		}
		if !rollbackFailed {
			cause = errors.Join(cause, os.Remove(journal))
		}
		return info, false, cause
	}
	for _, repo := range repos {
		renamed++ // include a command that succeeds immediately before cancellation
		if _, err := w.run(ctx, w.binary, "-C", repo.Path, "branch", "-m", branch); err != nil {
			result, _, rollbackErr := rollback(err)
			// Another project can share this repository but have a different gate.
			// If it claimed the branch after our preflight, retry a suffixed name.
			if !rollbackFailed && ctx.Err() == nil {
				if _, probeErr := w.run(ctx, w.binary, "-C", repo.RepoPath, "show-ref", "--verify", "refs/heads/"+branch); probeErr == nil {
					return result, true, nil
				}
			}
			return result, false, rollbackErr
		}
	}
	if err := moveTaskDirectory(oldPath, path); err != nil {
		return rollback(err)
	}
	info = target
	// Once moved, finish registration repair even if the request was cancelled.
	repairCtx, cancelRepair := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelRepair()
	info, repairErr := w.RepairTaskWorkspace(repairCtx, info)
	return info, false, repairErr
}

// RepairTaskWorkspace retains the existing names and never recreates a checkout.
func (w *Workspace) RepairTaskWorkspace(ctx context.Context, info ports.WorkspaceProjectInfo) (ports.WorkspaceProjectInfo, error) {
	root, err := w.validateManagedPath(info.Root.Path)
	if err != nil {
		return info, err
	}
	if err := validatePathComponent("project id", string(info.Root.ProjectID)); err != nil {
		return info, err
	}
	if filepath.Dir(root) != filepath.Join(w.managedRoot, string(info.Root.ProjectID)) {
		return info, errors.New("gitworktree: task path is outside its project")
	}
	repos := append([]ports.WorkspaceRepoInfo(nil), info.Worktrees...)
	if len(repos) == 0 {
		repo := info.Root.RepoPath
		if repo == "" {
			repo, err = w.repoPath(info.Root.ProjectID)
			if err != nil {
				return info, err
			}
		}
		repos = []ports.WorkspaceRepoInfo{{RepoPath: repo, Path: root}}
	}
	for _, repo := range repos {
		inside, err := pathWithin(root, repo.Path)
		if err != nil || !inside {
			return info, fmt.Errorf("gitworktree: task repository path is outside its workspace: %s", repo.Path)
		}
	}
	var repairErr error
	for i, repo := range repos {
		if _, err := w.run(ctx, w.binary, "-C", repo.RepoPath, "worktree", "repair", repo.Path); err != nil {
			repairErr = errors.Join(repairErr, err)
			continue
		}
		out, err := w.run(ctx, w.binary, "-C", repo.Path, "branch", "--show-current")
		repairErr = errors.Join(repairErr, err)
		if err == nil {
			branch := strings.TrimSpace(string(out))
			if i == 0 {
				info.Root.Branch = branch
			}
			if len(info.Worktrees) > i {
				info.Worktrees[i].Branch = branch
			}
		}
	}
	return info, repairErr
}
