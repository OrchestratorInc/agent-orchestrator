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
// directory. It refuses once the agent has left the branch or created refs
// under its namespace, because AO attributes those refs to the session by that
// namespace. Git never force-renames, and another claimant gets a suffix.
func (w *Workspace) RenameTaskBranch(ctx context.Context, info ports.WorkspaceProjectInfo, branch string) (ports.WorkspaceProjectInfo, error) {
	repos := append([]ports.WorkspaceRepoInfo(nil), info.Worktrees...)
	if len(repos) == 0 {
		repo := info.Root.RepoPath
		if repo == "" {
			var err error
			if repo, err = w.repoPath(info.Root.ProjectID); err != nil {
				return info, err
			}
		}
		repos = []ports.WorkspaceRepoInfo{{RepoPath: repo, Path: info.Root.Path, Branch: info.Root.Branch}}
	}
	if err := w.validateBranch(ctx, repos[0].RepoPath, branch); err != nil {
		return info, err
	}
	oldNamespace := strings.TrimSuffix(info.Root.Branch, "/root")
	refsByRepo := make([][]string, len(repos))
	for i, repo := range repos {
		head, err := w.run(ctx, w.binary, "-C", repo.Path, "symbolic-ref", "--quiet", "--short", "HEAD")
		if err != nil || strings.TrimSpace(string(head)) != repo.Branch {
			return info, fmt.Errorf("gitworktree: task checkout left its temporary branch %s", repo.Branch)
		}
		out, err := w.run(ctx, w.binary, "-C", repo.RepoPath, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
		if err != nil {
			return info, err
		}
		for _, ref := range strings.Fields(string(out)) {
			remote, pushed := strings.CutPrefix(ref, "refs/remotes/")
			if pushed {
				_, ref, _ = strings.Cut(remote, "/")
			} else {
				ref = strings.TrimPrefix(ref, "refs/heads/")
			}
			// A pushed temporary branch or any sibling already carries the old name.
			if (pushed || ref != repo.Branch) && (ref == repo.Branch || strings.HasPrefix(ref, oldNamespace+"/")) {
				return info, fmt.Errorf("gitworktree: task namespace %s already has %s", oldNamespace, ref)
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

func (w *Workspace) renameTaskCandidate(ctx context.Context, info ports.WorkspaceProjectInfo, repos []ports.WorkspaceRepoInfo, branch string) (ports.WorkspaceProjectInfo, bool, error) {
	for i, repo := range repos {
		_, err := w.run(ctx, w.binary, "-C", repo.Path, "branch", "-m", "--", repo.Branch, branch)
		if err == nil {
			continue
		}
		// All repositories share one task name, so undo the ones already renamed.
		if rollbackErr := w.undoTaskRenames(ctx, repos[:i], branch); rollbackErr != nil {
			return info, false, errors.Join(err, rollbackErr)
		}
		// Another project sharing this repository may have claimed the name.
		if _, probeErr := w.run(ctx, w.binary, "-C", repo.RepoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); probeErr == nil {
			return info, true, nil
		}
		return info, false, err
	}
	info.Root.Branch = branch
	info.Worktrees = append([]ports.WorkspaceRepoInfo(nil), info.Worktrees...)
	for i := range info.Worktrees {
		info.Worktrees[i].Branch = branch
	}
	return info, false, nil
}

func (w *Workspace) undoTaskRenames(ctx context.Context, renamed []ports.WorkspaceRepoInfo, branch string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for j := len(renamed) - 1; j >= 0; j-- {
		if _, err := w.run(ctx, w.binary, "-C", renamed[j].Path, "branch", "-m", "--", branch, renamed[j].Branch); err != nil {
			return err
		}
	}
	return nil
}
