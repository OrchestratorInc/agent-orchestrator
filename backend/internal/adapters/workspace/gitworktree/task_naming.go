package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// RenameTaskBranch gives a launched task's temporary branch its generated
// name. The checkout never moves, so a running agent keeps its working
// directory, and AO keeps attributing refs under the temporary namespace to
// the session. It refuses once the temporary branch is pushed, because the
// agent's later plain `git push` would then name a different remote branch.
// Git never force-renames, and another claimant gets a suffix.
func (w *Workspace) RenameTaskBranch(ctx context.Context, info ports.WorkspaceProjectInfo, branch string) (ports.WorkspaceProjectInfo, error) {
	repos, err := w.taskRepos(info)
	if err != nil {
		return info, err
	}
	if err := w.validateBranch(ctx, repos[0].RepoPath, branch); err != nil {
		return info, err
	}
	refsByRepo := make([][]string, len(repos))
	for i, repo := range repos {
		out, err := w.run(ctx, w.binary, "-C", repo.RepoPath, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
		if err != nil {
			return info, err
		}
		for _, ref := range strings.Fields(string(out)) {
			if remote, pushed := strings.CutPrefix(ref, "refs/remotes/"); pushed {
				if _, ref, _ = strings.Cut(remote, "/"); ref == repo.Branch {
					return info, fmt.Errorf("gitworktree: temporary task branch %s is already pushed", ref)
				}
			} else {
				ref = strings.TrimPrefix(ref, "refs/heads/")
			}
			refsByRepo[i] = append(refsByRepo[i], ref)
		}
	}
	namespace := strings.TrimSuffix(branch, "/root")
	root := branch[len(namespace):] // keep a bare workspace-project branch bare
candidates:
	for suffix := 1; suffix <= 100; suffix++ {
		candidate := namespace
		if suffix > 1 {
			candidate = fmt.Sprintf("%s-%d", namespace, suffix)
		}
		if candidate+root == info.Root.Branch {
			return info, nil // the generated name is the temporary one
		}
		for _, refs := range refsByRepo {
			for _, ref := range refs {
				if ref == candidate || strings.HasPrefix(ref, candidate+"/") || strings.HasPrefix(candidate, ref+"/") {
					continue candidates
				}
			}
		}
		renamed, retry, err := w.renameTaskCandidate(ctx, info, repos, candidate+root)
		if retry {
			continue
		}
		return renamed, err
	}
	return info, fmt.Errorf("gitworktree: no unused task branch for %q", branch)
}

// RevertTaskBranch puts back the branches original held before a rename the
// caller could not record. It is exact: no suffix and no guard.
func (w *Workspace) RevertTaskBranch(ctx context.Context, renamed, original ports.WorkspaceProjectInfo) error {
	from, err := w.taskRepos(renamed)
	if err != nil {
		return err
	}
	to, err := w.taskRepos(original)
	if err != nil || len(to) != len(from) {
		return errors.Join(err, errors.New("gitworktree: task repositories changed during naming"))
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for i, repo := range from {
		if _, err := w.run(ctx, w.binary, "-C", repo.Path, "branch", "-m", "--", repo.Branch, to[i].Branch); err != nil {
			return err
		}
	}
	return nil
}

func (w *Workspace) taskRepos(info ports.WorkspaceProjectInfo) ([]ports.WorkspaceRepoInfo, error) {
	if len(info.Worktrees) > 0 {
		return append([]ports.WorkspaceRepoInfo(nil), info.Worktrees...), nil
	}
	repo := info.Root.RepoPath
	if repo == "" {
		var err error
		if repo, err = w.repoPath(info.Root.ProjectID); err != nil {
			return nil, err
		}
	}
	return []ports.WorkspaceRepoInfo{{RepoPath: repo, Path: info.Root.Path, Branch: info.Root.Branch}}, nil
}

func (w *Workspace) renameTaskCandidate(ctx context.Context, info ports.WorkspaceProjectInfo, repos []ports.WorkspaceRepoInfo, branch string) (ports.WorkspaceProjectInfo, bool, error) {
	for i, repo := range repos {
		_, err := w.run(ctx, w.binary, "-C", repo.Path, "branch", "-m", "--", repo.Branch, branch)
		if err == nil {
			continue
		}
		// All repositories share one task name, so undo the ones already renamed.
		if left, rollbackErr := w.undoTaskRenames(ctx, repos[:i], branch); rollbackErr != nil {
			// Report what Git now holds: the first left repositories kept the new name.
			info = renamedTaskInfo(info, branch, left)
			return info, false, errors.Join(err, rollbackErr)
		}
		// Another project sharing this repository may have claimed the name.
		if _, probeErr := w.run(ctx, w.binary, "-C", repo.RepoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); probeErr == nil {
			return info, true, nil
		}
		return info, false, err
	}
	return renamedTaskInfo(info, branch, len(repos)), false, nil
}

// renamedTaskInfo marks the first n repositories (and the root with the first)
// as carrying branch.
func renamedTaskInfo(info ports.WorkspaceProjectInfo, branch string, n int) ports.WorkspaceProjectInfo {
	if n > 0 {
		info.Root.Branch = branch
	}
	info.Worktrees = append([]ports.WorkspaceRepoInfo(nil), info.Worktrees...)
	for i := range info.Worktrees[:min(n, len(info.Worktrees))] {
		info.Worktrees[i].Branch = branch
	}
	return info
}

// undoTaskRenames renames branch back in reverse order and reports how many
// leading repositories still carry branch when it stops.
func (w *Workspace) undoTaskRenames(ctx context.Context, renamed []ports.WorkspaceRepoInfo, branch string) (int, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for j := len(renamed) - 1; j >= 0; j-- {
		if _, err := w.run(ctx, w.binary, "-C", renamed[j].Path, "branch", "-m", "--", branch, renamed[j].Branch); err != nil {
			return j + 1, err
		}
	}
	return 0, nil
}
