package session

import (
	"context"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// branchStateWriter is the narrow store write for observed branch facts.
type branchStateWriter interface {
	SetSessionBranchState(ctx context.Context, id domain.SessionID, state domain.SessionBranchState) (bool, error)
}

// ReconcileSessionBranchState reads the session branch's commit and push facts
// from git and persists them when they changed, so every client reads one
// observed truth instead of computing git state itself. A git failure keeps the
// last known facts: a failed read is not evidence that nothing was pushed.
func (s *Service) ReconcileSessionBranchState(ctx context.Context, id domain.SessionID) error {
	writer, ok := s.store.(branchStateWriter)
	if !ok {
		return nil
	}
	// Serialized so an older read can never persist over a newer one when the
	// observer tick and a workspace change reconcile the same session at once.
	s.branchStateMu.Lock()
	defer s.branchStateMu.Unlock()
	rec, found, err := s.store.GetSession(ctx, id)
	if err != nil || !found {
		return err
	}
	if rec.IsTerminated || rec.Metadata.WorkspacePath == "" || isStandaloneScratchWorkspace(rec) {
		return nil
	}
	project, projectOK, err := s.sessionProject(ctx, rec)
	if err != nil {
		return err
	}
	if projectOK && project.Kind.WithDefault() == domain.ProjectKindWorkspace {
		return nil
	}
	prs, err := s.workspaceComparePRs(ctx, rec.ID)
	if err != nil {
		return err
	}
	root := rec.Metadata.WorkspacePath
	compare := resolveWorkspaceCompare(ctx, root, rec.Metadata.DiffBaseSHA, rec.Metadata.DiffBaseRef, defaultBranchForProject(project, projectOK), prs)
	state, ok := observeBranchState(ctx, root, compare.gitBase())
	if !ok || (rec.BranchState != nil && *rec.BranchState == state) {
		return nil
	}
	_, err = writer.SetSessionBranchState(ctx, id, state)
	return err
}

// observeBranchState counts the branch's commits on top of base and how many
// of them are missing from the branch's remote-tracking ref. It reports false
// when git cannot answer.
func observeBranchState(ctx context.Context, root, base string) (domain.SessionBranchState, bool) {
	var state domain.SessionBranchState
	if base = strings.TrimSpace(base); base != "" && base != "HEAD" {
		commits, ok := gitRevListCount(ctx, root, base+"..HEAD")
		if !ok {
			return state, false
		}
		state.Commits = commits
	}
	state.Unpushed = state.Commits
	branchOut, err := gitWorkspaceOutput(ctx, root, "symbolic-ref", "-q", "--short", "HEAD")
	branch := strings.TrimSpace(branchOut)
	if err != nil || branch == "" {
		// Detached HEAD has no branch to push.
		return state, true
	}
	// The branch's own name on its remote, not its configured upstream: AO
	// worktrees track the base (origin/main) until the first push.
	remoteOut, _ := gitWorkspaceOutput(ctx, root, "config", "--get", "branch."+branch+".remote")
	remote := strings.TrimSpace(remoteOut)
	if remote == "" || remote == "." {
		remote = "origin"
	}
	remoteBranch := remote + "/" + branch
	if !gitCommitExists(ctx, root, "refs/remotes/"+remoteBranch) {
		return state, true
	}
	unpushed, ok := gitRevListCount(ctx, root, "refs/remotes/"+remoteBranch+"..HEAD")
	if !ok {
		return state, false
	}
	state.RemoteBranch = remoteBranch
	state.Unpushed = unpushed
	return state, true
}

func gitRevListCount(ctx context.Context, root, rangeSpec string) (int, bool) {
	out, err := gitWorkspaceOutput(ctx, root, "rev-list", "--count", rangeSpec)
	if err != nil {
		return 0, false
	}
	count, err := strconv.Atoi(strings.TrimSpace(out))
	return count, err == nil
}
