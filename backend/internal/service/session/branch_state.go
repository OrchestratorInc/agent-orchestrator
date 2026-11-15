package session

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type branchStateWriter interface {
	SetSessionBranchState(ctx context.Context, id domain.SessionID, state domain.SessionBranchState) (bool, error)
}

const branchStateRemote = "origin"

type branchTarget struct {
	rec      domain.SessionRecord
	branch   string
	head     string
	baseRefs []string
	baseSHA  string
}

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
		return nil
	}
	base := target.baseSHA
	baseRef := ""
	for _, ref := range target.baseRefs {
		if sha := tips[ref]; sha != "" {
			base, baseRef = sha, ref
			break
		}
	}
	rec := target.rec
	fingerprint := target.branch + " " + local + " " + remote + " " + base + " " + baseRef
	if rec.BranchState != nil && s.branchTips[rec.ID] == fingerprint {
		return nil
	}

	root := rec.Metadata.WorkspacePath
	state := domain.SessionBranchState{Branch: target.branch, Head: local}
	switch {
	case base != "" && baseRef != "":
		ahead, behind, ok := gitLeftRightCount(ctx, root, local, base)
		if !ok {
			return nil
		}
		state.Commits, state.Behind = ahead, behind
		state.Base, state.BaseRef = branchBaseNames(baseRef)
		if ahead > 0 && behind > 0 {
			files, ok := gitMergeConflictFiles(ctx, root, base, local)
			state.Conflicts = ok && len(files) > 0
		}
	case base != "":
		commits, ok := gitRevListCount(ctx, root, base+".."+local)
		if !ok {
			return nil
		}
		state.Commits = commits
	}
	switch remote {
	case "":
		state.Unpushed = state.Commits
	case local:
		state.RemoteBranch = branchStateRemote + "/" + target.branch
		state.RemoteHead = remote
	default:
		unpushed, ok := gitRevListCount(ctx, root, remote+".."+local)
		if !ok {
			return nil
		}
		state.RemoteBranch = branchStateRemote + "/" + target.branch
		state.RemoteHead = remote
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

func branchBaseNames(ref string) (name, display string) {
	if rest, ok := strings.CutPrefix(ref, "refs/remotes/"); ok {
		if _, branch, ok := strings.Cut(rest, "/"); ok {
			return branch, rest
		}
		return rest, rest
	}
	name = strings.TrimPrefix(ref, "refs/heads/")
	return name, name
}

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

func gitLeftRightCount(ctx context.Context, root, head, base string) (ahead, behind int, ok bool) {
	out, err := gitWorkspaceOutput(ctx, root, "rev-list", "--left-right", "--count", head+"..."+base)
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, false
	}
	ahead, aheadErr := strconv.Atoi(fields[0])
	behind, behindErr := strconv.Atoi(fields[1])
	return ahead, behind, aheadErr == nil && behindErr == nil
}
