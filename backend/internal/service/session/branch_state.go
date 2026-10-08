package session

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// branchStateWriter is the narrow store write for observed branch facts.
type branchStateWriter interface {
	SetSessionBranchState(ctx context.Context, id domain.SessionID, state domain.SessionBranchState) (bool, error)
}

// branchStateRemote is the remote AO pushes session branches to.
const branchStateRemote = "origin"

type branchTarget struct {
	rec      domain.SessionRecord
	branch   string // empty on a detached HEAD
	head     string // detached HEAD commit
	baseRefs []string
	baseSHA  string
}

// ReconcileBranchStates refreshes every live session's persisted branch facts.
// It runs on the SCM observer's tick: one git for-each-ref per repository reads
// every session's branch, remote, and base tips, and git counts commits only
// for sessions whose tips moved since the last pass.
func (s *Service) ReconcileBranchStates(ctx context.Context) error {
	writer, ok := s.store.(branchStateWriter)
	if !ok {
		return nil
	}
	recs, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	return s.reconcileBranchStates(ctx, writer, recs, true)
}

// ReconcileSessionBranchState is the single-session pass a workspace watch
// runs when it sees a change.
func (s *Service) ReconcileSessionBranchState(ctx context.Context, id domain.SessionID) error {
	writer, ok := s.store.(branchStateWriter)
	if !ok {
		return nil
	}
	rec, found, err := s.store.GetSession(ctx, id)
	if err != nil || !found {
		return err
	}
	return s.reconcileBranchStates(ctx, writer, []domain.SessionRecord{rec}, false)
}

func (s *Service) reconcileBranchStates(ctx context.Context, writer branchStateWriter, recs []domain.SessionRecord, full bool) error {
	// Serialized so an older read can never persist over a newer one when the
	// tick and a workspace change reconcile the same session at once.
	s.branchStateMu.Lock()
	defer s.branchStateMu.Unlock()
	if s.branchTips == nil {
		s.branchTips = map[domain.SessionID]string{}
	}

	type projectLookup struct {
		rec domain.ProjectRecord
		ok  bool
	}
	projects := map[domain.ProjectID]projectLookup{}
	repos := map[string][]branchTarget{}
	live := map[domain.SessionID]bool{}
	for _, rec := range recs {
		if rec.IsTerminated || rec.Metadata.WorkspacePath == "" || isStandaloneScratchWorkspace(rec) {
			continue
		}
		live[rec.ID] = true
		project, seen := projects[rec.ProjectID]
		if !seen {
			p, ok, err := s.sessionProject(ctx, rec)
			if err != nil {
				return err
			}
			project = projectLookup{rec: p, ok: ok}
			projects[rec.ProjectID] = project
		}
		if project.ok && project.rec.Kind.WithDefault() == domain.ProjectKindWorkspace {
			continue
		}
		commonDir, branch, head, ok := readGitHead(rec.Metadata.WorkspacePath)
		if !ok {
			continue
		}
		repos[commonDir] = append(repos[commonDir], branchTarget{
			rec:      rec,
			branch:   branch,
			head:     head,
			baseRefs: branchBaseRefs(rec.Metadata.DiffBaseRef, defaultBranchForProject(project.rec, project.ok)),
			baseSHA:  strings.TrimSpace(rec.Metadata.DiffBaseSHA),
		})
	}
	if full {
		for id := range s.branchTips {
			if !live[id] {
				delete(s.branchTips, id)
			}
		}
	}

	for _, targets := range repos {
		tips, ok := readRefTips(ctx, targets)
		if !ok {
			continue
		}
		for _, target := range targets {
			if err := s.reconcileBranchTarget(ctx, writer, target, tips); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) reconcileBranchTarget(ctx context.Context, writer branchStateWriter, target branchTarget, tips map[string]string) error {
	local := target.head
	remote := ""
	if target.branch != "" {
		local = tips["refs/heads/"+target.branch]
		remote = tips["refs/remotes/"+branchStateRemote+"/"+target.branch]
	}
	if local == "" {
		return nil // unborn branch
	}
	base := target.baseSHA
	for _, ref := range target.baseRefs {
		if sha := tips[ref]; sha != "" {
			base = sha
			break
		}
	}
	rec := target.rec
	fingerprint := local + " " + remote + " " + base
	if rec.BranchState != nil && s.branchTips[rec.ID] == fingerprint {
		return nil
	}

	root := rec.Metadata.WorkspacePath
	var state domain.SessionBranchState
	if base != "" {
		commits, ok := gitRevListCount(ctx, root, base+".."+local)
		if !ok {
			// A failed read is not evidence that nothing was committed.
			return nil
		}
		state.Commits = commits
	}
	switch remote {
	case "":
		state.Unpushed = state.Commits
	case local:
		state.RemoteBranch = branchStateRemote + "/" + target.branch
	default:
		unpushed, ok := gitRevListCount(ctx, root, remote+".."+local)
		if !ok {
			return nil
		}
		state.RemoteBranch = branchStateRemote + "/" + target.branch
		state.Unpushed = unpushed
	}
	if rec.BranchState == nil || *rec.BranchState != state {
		if _, err := writer.SetSessionBranchState(ctx, rec.ID, state); err != nil {
			return err
		}
	}
	s.branchTips[rec.ID] = fingerprint
	return nil
}

// readRefTips lists the branch, remote, and base tips of every target in one
// repository with a single git process.
func readRefTips(ctx context.Context, targets []branchTarget) (map[string]string, bool) {
	args := []string{"for-each-ref", "--format=%(objectname) %(refname)"}
	seen := map[string]bool{}
	add := func(ref string) {
		if !seen[ref] {
			seen[ref] = true
			args = append(args, ref)
		}
	}
	for _, target := range targets {
		if target.branch != "" {
			add("refs/heads/" + target.branch)
			add("refs/remotes/" + branchStateRemote + "/" + target.branch)
		}
		for _, ref := range target.baseRefs {
			add(ref)
		}
	}
	out, err := gitWorkspaceOutput(ctx, targets[0].rec.Metadata.WorkspacePath, args...)
	if err != nil {
		return nil, false
	}
	tips := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if sha, ref, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			tips[ref] = sha
		}
	}
	return tips, true
}

// branchBaseRefs returns the full ref names the session's diff base resolves
// from, in the same preference order the Files tab uses.
func branchBaseRefs(recordedRef, defaultBranch string) []string {
	ref := strings.TrimSpace(recordedRef)
	if ref == "" || ref == "HEAD" {
		ref = defaultBranch
	}
	candidates := workspaceBaseRefCandidates(ref)
	refs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		switch {
		case strings.HasPrefix(candidate, "refs/"):
		case strings.HasPrefix(candidate, "origin/"):
			candidate = "refs/remotes/" + candidate
		default:
			candidate = "refs/heads/" + candidate
		}
		refs = append(refs, candidate)
	}
	return refs
}

// readGitHead reads a worktree's common git dir and HEAD from the files git
// itself reads, so finding them costs no git process.
func readGitHead(root string) (commonDir, branch, head string, ok bool) {
	gitDir := filepath.Join(root, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return "", "", "", false
	}
	if !info.IsDir() {
		raw, err := os.ReadFile(gitDir)
		if err != nil {
			return "", "", "", false
		}
		path, found := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
		if !found {
			return "", "", "", false
		}
		gitDir = filepath.FromSlash(strings.TrimSpace(path))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(root, gitDir)
		}
	}
	commonDir = gitDir
	if raw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = filepath.FromSlash(strings.TrimSpace(string(raw)))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
	}
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", "", "", false
	}
	text := strings.TrimSpace(string(raw))
	if ref, symbolic := strings.CutPrefix(text, "ref: "); symbolic {
		branch, ok = strings.CutPrefix(ref, "refs/heads/")
		return filepath.Clean(commonDir), branch, "", ok
	}
	return filepath.Clean(commonDir), "", text, text != ""
}

func gitRevListCount(ctx context.Context, root, rangeSpec string) (int, bool) {
	out, err := gitWorkspaceOutput(ctx, root, "rev-list", "--count", rangeSpec)
	if err != nil {
		return 0, false
	}
	count, err := strconv.Atoi(strings.TrimSpace(out))
	return count, err == nil
}
