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
	root := branch[len(namespace):]
candidates:
	for suffix := 1; suffix <= 100; suffix++ {
		candidate := namespace
		if suffix > 1 {
			candidate = fmt.Sprintf("%s-%d", namespace, suffix)
		}
		// The generated name is the temporary one; a workspace project's
		// temporary branch is the bare namespace.
		if candidate == strings.TrimSuffix(info.Root.Branch, "/root") {
			return info, nil
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
// caller could not record. It is exact: no suffix and no guard. On error it
// reports the branches Git holds: the first repositories are already back.
func (w *Workspace) RevertTaskBranch(ctx context.Context, renamed, original ports.WorkspaceProjectInfo) (ports.WorkspaceProjectInfo, error) {
	from, err := w.taskRepos(renamed)
	if err != nil {
		return renamed, err
	}
	to, err := w.taskRepos(original)
	if err != nil || len(to) != len(from) {
		return renamed, errors.Join(err, errors.New("gitworktree: task repositories changed during naming"))
	}
	moves := make([]branchMove, len(from))
	for i, repo := range from {
		moves[i] = branchMove{repo.Path, repo.Branch, to[i].Branch}
	}
	n, err := w.moveBranches(ctx, moves)
	return withTaskBranches(renamed, moves[:n]), err
}

func (w *Workspace) taskRepos(info ports.WorkspaceProjectInfo) ([]ports.WorkspaceRepoInfo, error) {
	if len(info.Worktrees) > 0 {
		return info.Worktrees, nil
	}
	repo, err := w.repoPathForInfo(info.Root)
	if err != nil {
		return nil, err
	}
	return []ports.WorkspaceRepoInfo{{RepoPath: repo, Path: info.Root.Path, Branch: info.Root.Branch}}, nil
}

func (w *Workspace) renameTaskCandidate(ctx context.Context, info ports.WorkspaceProjectInfo, repos []ports.WorkspaceRepoInfo, branch string) (ports.WorkspaceProjectInfo, bool, error) {
	moves := make([]branchMove, len(repos))
	for i, repo := range repos {
		moves[i] = branchMove{repo.Path, repo.Branch, branch}
	}
	n, err := w.moveBranches(ctx, moves)
	if err == nil {
		return withTaskBranches(info, moves), false, nil
	}
	// All repositories share one task name, so undo the ones already renamed,
	// last first.
	undo := make([]branchMove, n)
	for i := range undo {
		mv := moves[n-1-i]
		undo[i] = branchMove{mv.path, mv.to, mv.from}
	}
	if undone, undoErr := w.moveBranches(ctx, undo); undoErr != nil {
		// Report what Git now holds: the first repositories kept the new name.
		return withTaskBranches(info, moves[:n-undone]), false, errors.Join(err, undoErr)
	}
	// Another project sharing this repository may have claimed the name.
	if claimed, _ := w.refExists(ctx, repos[n].RepoPath, "refs/heads/"+branch); claimed {
		return info, true, nil
	}
	return info, false, err
}

// withTaskBranches puts the first repositories (and the root with the first)
// on the branches the landed moves gave them.
func withTaskBranches(info ports.WorkspaceProjectInfo, landed []branchMove) ports.WorkspaceProjectInfo {
	if len(landed) > 0 {
		info.Root.Branch = landed[0].to
	}
	info.Worktrees = append([]ports.WorkspaceRepoInfo(nil), info.Worktrees...)
	for i := range info.Worktrees[:min(len(landed), len(info.Worktrees))] {
		info.Worktrees[i].Branch = landed[i].to
	}
	return info
}

type branchMove struct{ path, from, to string }

// moveBranches renames branches in order and reports how many landed. Shutdown
// cannot cancel it, so Git never stops halfway through renaming the branch a
// live agent has checked out.
func (w *Workspace) moveBranches(ctx context.Context, moves []branchMove) (int, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for i, mv := range moves {
		if _, err := w.run(ctx, w.binary, "-C", mv.path, "branch", "-m", "--", mv.from, mv.to); err != nil {
			return i, err
		}
	}
	return len(moves), nil
}
