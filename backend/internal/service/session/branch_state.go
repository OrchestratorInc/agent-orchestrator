package session

import (
	"context"
	"fmt"
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
	baseRef := ""
	for _, ref := range target.baseRefs {
		if sha := tips[ref]; sha != "" {
			base, baseRef = sha, ref
			break
		}
	}
	rec := target.rec
	// With no origin branch of the same name, the session may be continuing a
	// PR whose head lives elsewhere (a contributor's fork, or another branch
	// name). That PR's recorded head is where this branch was pushed.
	var prHead domain.PullRequest
	if remote == "" {
		pr, ok, err := s.openPRUnder(ctx, rec.Metadata.WorkspacePath, rec.ID, local)
		if err != nil {
			// A failed read is not evidence that nothing was pushed: keep the last
			// state rather than report every commit unpushed, and let the next
			// pass retry instead of failing every other session's reconcile.
			if s.logger != nil {
				s.logger.Warn("read session PRs for branch state", "session", rec.ID, "err", err)
			}
			return nil
		}
		if ok {
			prHead = pr
		}
	}
	// The PR's number and source branch name RemoteBranch, so a different PR at
	// the same head, or a renamed source branch, must also re-read.
	fingerprint := target.branch + " " + local + " " + remote + " " + base + " " + baseRef + " " +
		prHead.HeadSHA + " " + strconv.Itoa(prHead.Number) + " " + prHead.SourceBranch
	if rec.BranchState != nil && s.branchTips[rec.ID] == fingerprint {
		return nil
	}

	root := rec.Metadata.WorkspacePath
	state := domain.SessionBranchState{Branch: target.branch, Head: local}
	switch {
	case base != "" && baseRef != "":
		// One call counts both sides, so reading how far behind the base the
		// branch is costs no extra git process.
		ahead, behind, ok := gitLeftRightCount(ctx, root, local, base)
		if !ok {
			// A failed read is not evidence that nothing was committed.
			return nil
		}
		state.Commits, state.Behind = ahead, behind
		state.Base, state.BaseRef = branchBaseNames(baseRef)
		// Both sides must have moved since they split for a merge to conflict.
		// merge-tree needs git 2.38; an older git leaves conflicts unreported
		// rather than dropping the other facts. The tips fingerprint above keeps
		// this to one in-memory merge per new commit on either side.
		if ahead > 0 && behind > 0 {
			files, ok := gitMergeConflictFiles(ctx, root, base, local)
			state.Conflicts = ok && len(files) > 0
		}
	case base != "":
		// Only a recorded commit: there is no base branch to be behind.
		commits, ok := gitRevListCount(ctx, root, base+".."+local)
		if !ok {
			return nil
		}
		state.Commits = commits
	}
	switch {
	case remote == "" && prHead.HeadSHA != "":
		state.RemoteBranch = fmt.Sprintf("pr-%d/%s", prHead.Number, prHead.SourceBranch)
		state.RemoteHead = prHead.HeadSHA
		if prHead.HeadSHA != local {
			unpushed, ok := gitRevListCount(ctx, root, prHead.HeadSHA+".."+local)
			if !ok {
				return nil
			}
			state.Unpushed = unpushed
		}
	case remote == "":
		state.Unpushed = state.Commits
	case remote == local:
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

// branchBaseNames splits a full base ref into its short branch name and the
// ref as shown to people: "refs/remotes/origin/main" is "main" read as
// "origin/main", and "refs/heads/main" is "main" read as "main".
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

// gitLeftRightCount counts the commits on head that base lacks and the commits
// on base that head lacks.
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

// openPRUnder returns the session's open PR whose recorded head is local or one
// of its ancestors, so commits already on that PR do not read as unpushed. A
// PR at some other commit (another branch the session also opened) does not
// count. It reads the store only for branches with no same-named origin branch.
func (s *Service) openPRUnder(ctx context.Context, root string, id domain.SessionID, local string) (domain.PullRequest, bool, error) {
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return domain.PullRequest{}, false, err
	}
	var under []domain.PullRequest
	for _, pr := range prs {
		if pr.Merged || pr.Closed || pr.HeadSHA == "" || pr.SourceBranch == "" {
			continue
		}
		if pr.HeadSHA == local {
			return pr, true, nil
		}
		under = append(under, pr)
	}
	for _, pr := range under {
		if gitIsAncestor(ctx, root, pr.HeadSHA, local) {
			return pr, true, nil
		}
	}
	return domain.PullRequest{}, false, nil
}
