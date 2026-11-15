package session

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/gitdefault"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const maxRecentCommits = 50

type WorkspaceView struct {
	Branch          string
	PRNumber        int
	SourceURL       string
	ExpectedTipSHA  string
	ExpectedBaseSHA string
}

type WorkspaceViewKind string

const (
	WorkspaceViewCheckedOut  WorkspaceViewKind = "checked_out"
	WorkspaceViewBranch      WorkspaceViewKind = "branch"
	WorkspaceViewPullRequest WorkspaceViewKind = "pull_request"
)

type WorkspaceRangeMode string

const (
	WorkspaceRangeSinceBase WorkspaceRangeMode = "since_base"
	WorkspaceRangeRecent    WorkspaceRangeMode = "recent"
)

type WorkspaceViewInfo struct {
	Kind      WorkspaceViewKind
	Name      string
	TipSHA    string
	BaseSHA   string
	BaseRef   string
	RangeMode WorkspaceRangeMode
}

type resolvedWorkspaceView struct {
	WorkspaceViewInfo
	root        string
	rangeBefore string
	logArgs     []string
	maxCommits  int
}

func (s *Service) resolveWorkspaceView(ctx context.Context, id domain.SessionID, v WorkspaceView) (resolvedWorkspaceView, error) {
	if err := validateWorkspaceView(v); err != nil {
		return resolvedWorkspaceView{}, err
	}
	if v.PRNumber > 0 {
		view, err := s.resolvePRView(ctx, id, v.PRNumber, v.SourceURL)
		if err != nil {
			return resolvedWorkspaceView{}, err
		}
		return view, fenceWorkspaceView(view, v)
	}
	rec, err := s.sessionWorkspaceRecord(ctx, id)
	if err != nil {
		return resolvedWorkspaceView{}, err
	}
	project, projectOK, err := s.sessionProject(ctx, rec)
	if err != nil {
		return resolvedWorkspaceView{}, err
	}
	singleRepo := !isStandaloneScratchWorkspace(rec) && (!projectOK || project.Kind.WithDefault() == domain.ProjectKindSingleRepo)
	if v.Branch == "" {
		view, err := s.resolveCheckedOutView(ctx, rec, project, projectOK, singleRepo)
		if err != nil {
			return resolvedWorkspaceView{}, err
		}
		return view, fenceWorkspaceView(view, v)
	}
	if !singleRepo {
		return resolvedWorkspaceView{}, apierr.Invalid("WORKSPACE_VIEW_UNSUPPORTED", "Only single-repository sessions can review another branch", nil)
	}
	view, err := resolveBranchView(ctx, rec.Metadata.WorkspacePath, v.Branch, project, projectOK)
	if err != nil {
		return resolvedWorkspaceView{}, err
	}
	return view, fenceWorkspaceView(view, v)
}

func validateWorkspaceView(v WorkspaceView) error {
	switch {
	case v.Branch != "" && v.PRNumber != 0:
		return apierr.Invalid("WORKSPACE_VIEW_AMBIGUOUS", "Choose a branch or a pull request, not both", nil)
	case v.PRNumber < 0:
		return apierr.Invalid("INVALID_WORKSPACE_VIEW", "prNumber must be a positive integer", nil)
	case v.SourceURL != "" && v.PRNumber == 0:
		return apierr.Invalid("INVALID_WORKSPACE_VIEW", "sourceUrl requires prNumber", nil)
	case v.Branch != "" && !validWorkspaceBranchName(v.Branch):
		return apierr.Invalid("INVALID_WORKSPACE_BRANCH", "Branch name is not valid", nil)
	}
	return nil
}

func fenceWorkspaceView(view resolvedWorkspaceView, v WorkspaceView) error {
	if (v.ExpectedTipSHA != "" && v.ExpectedTipSHA != view.TipSHA) || (v.ExpectedBaseSHA != "" && v.ExpectedBaseSHA != view.BaseSHA) {
		return apierr.Conflict("WORKSPACE_REF_MOVED", "The reviewed branch moved; reload its commits", map[string]any{"tipSha": view.TipSHA, "baseSha": view.BaseSHA})
	}
	return nil
}

func validWorkspaceBranchName(name string) bool {
	if name == "" || len(name) > 255 || name == "@" {
		return false
	}
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") {
		return false
	}
	if strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "//") {
		return false
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < 0x20 || c == 0x7f || strings.IndexByte(" ~^:?*[\\", c) >= 0 {
			return false
		}
	}
	for _, component := range strings.Split(name, "/") {
		if strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

func (s *Service) resolveCheckedOutView(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, projectOK, singleRepo bool) (resolvedWorkspaceView, error) {
	view := resolvedWorkspaceView{
		WorkspaceViewInfo: WorkspaceViewInfo{Kind: WorkspaceViewCheckedOut, Name: rec.Metadata.Branch, RangeMode: WorkspaceRangeSinceBase},
		maxCommits:        maxCommitLogCommits,
	}
	if !singleRepo {
		return view, nil
	}
	root := rec.Metadata.WorkspacePath
	prs, err := s.workspaceComparePRs(ctx, rec.ID)
	if err != nil {
		return resolvedWorkspaceView{}, err
	}
	resolve := func(rctx context.Context) workspaceCompareTarget {
		return resolveWorkspaceCompare(rctx, root, rec.Metadata.DiffBaseSHA, rec.Metadata.DiffBaseRef, defaultBranchForProject(project, projectOK), prs)
	}
	compare, _, err := s.resolveWorkspaceChanges(ctx, rec.ID, root, resolve)
	if err != nil {
		return resolvedWorkspaceView{}, err
	}
	view.root = root
	view.BaseRef = compare.BaseRef
	view.TipSHA, _ = gitRevision(ctx, root, "HEAD")
	if base := strings.TrimSpace(compare.gitBase()); base != "" && base != "HEAD" && view.TipSHA != "" {
		view.BaseSHA = base
		view.rangeBefore = base
		view.logArgs = []string{base + ".." + view.TipSHA}
	}
	return view, nil
}

func resolveBranchView(ctx context.Context, root, name string, project domain.ProjectRecord, projectOK bool) (resolvedWorkspaceView, error) {
	ref := "refs/heads/" + name
	if strings.HasPrefix(name, "refs/remotes/") {
		ref = name
	}
	out, err := gitWorkspaceOutput(ctx, root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil && (errors.Is(err, ports.ErrWorkspaceRepoUnavailable) || ctx.Err() != nil) {
		return resolvedWorkspaceView{}, err
	}
	tip := strings.TrimSpace(out)
	if err != nil || tip == "" {
		return resolvedWorkspaceView{}, apierr.NotFound("WORKSPACE_BRANCH_NOT_FOUND", "Branch not found")
	}
	view := resolvedWorkspaceView{WorkspaceViewInfo: WorkspaceViewInfo{Kind: WorkspaceViewBranch, Name: name, TipSHA: tip}, root: root}
	defaultName, defaultRef, defaultSHA := viewDefaultBranch(ctx, root, project, projectOK)
	if name != defaultName && defaultSHA != "" {
		if base, ok := gitMergeBaseOf(ctx, root, defaultSHA, tip); ok {
			view.BaseSHA, view.BaseRef, view.RangeMode = base, defaultRef, WorkspaceRangeSinceBase
			view.rangeBefore = base
			view.logArgs = []string{base + ".." + tip}
			view.maxCommits = maxCommitLogCommits
			return view, nil
		}
	}
	before, err := recentRangeBefore(ctx, root, tip)
	if err != nil {
		return resolvedWorkspaceView{}, err
	}
	view.RangeMode = WorkspaceRangeRecent
	view.rangeBefore = before
	view.logArgs = []string{"--first-parent", tip}
	view.maxCommits = maxRecentCommits
	return view, nil
}

func viewDefaultBranch(ctx context.Context, root string, project domain.ProjectRecord, projectOK bool) (name, ref, sha string) {
	name, inspectedRef, ok := viewDefaultBranchName(ctx, root, project, projectOK)
	if !ok {
		return "", "", ""
	}
	candidates := append([]string{inspectedRef}, workspaceBaseRefCandidates(name)...)
	for _, candidate := range candidates {
		if candidate == "" || strings.HasPrefix(candidate, "-") {
			continue
		}
		if sha, ok := gitRevision(ctx, root, candidate+"^{commit}"); ok {
			return name, candidate, sha
		}
	}
	return name, "", ""
}

func viewDefaultBranchName(ctx context.Context, root string, project domain.ProjectRecord, projectOK bool) (name, inspectedRef string, ok bool) {
	if name := workspaceDefaultBranch(defaultBranchForProject(project, projectOK)); name != "" {
		return name, "", true
	}
	resolution, err := gitdefault.New("git", nil).Inspect(ctx, root)
	if err != nil {
		return "", "", false
	}
	return resolution.Branch, resolution.Ref, true
}

func recentRangeBefore(ctx context.Context, root, tip string) (string, error) {
	out, err := gitWorkspaceOutput(ctx, root, "rev-list", "--first-parent", "--max-count="+strconv.Itoa(maxRecentCommits+1), tip)
	if err != nil {
		return "", err
	}
	if shas := strings.Fields(out); len(shas) > maxRecentCommits {
		return shas[maxRecentCommits], nil
	}
	if tree, ok := workspaceEmptyTree(ctx, root); ok {
		return tree, nil
	}
	return "", fmt.Errorf("resolve the empty tree in %s", root)
}

func (s *Service) resolvePRView(ctx context.Context, id domain.SessionID, number int, sourceURL string) (resolvedWorkspaceView, error) {
	rec, pr, err := s.prFileSource(ctx, id, number, sourceURL)
	if err != nil {
		return resolvedWorkspaceView{}, err
	}
	root := rec.Metadata.WorkspacePath
	base, ok := gitMergeBaseOf(ctx, root, pr.BaseSHA, pr.HeadSHA)
	if !ok {
		return resolvedWorkspaceView{}, unavailablePRSource()
	}
	return resolvedWorkspaceView{
		WorkspaceViewInfo: WorkspaceViewInfo{Kind: WorkspaceViewPullRequest, Name: pr.SourceBranch, TipSHA: pr.HeadSHA, BaseSHA: base, BaseRef: pr.TargetBranch, RangeMode: WorkspaceRangeSinceBase},
		root:              root,
		rangeBefore:       base,
		logArgs:           []string{base + ".." + pr.HeadSHA},
		maxCommits:        maxCommitLogCommits,
	}, nil
}

func workspaceViewHistory(ctx context.Context, id domain.SessionID, view resolvedWorkspaceView) (WorkspaceHistory, error) {
	var commits []CommitSummary
	var truncated bool
	var summary WorkspaceSummary
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		commits, truncated, err = revisionCommitList(gctx, view.root, view.logArgs, view.maxCommits)
		return err
	})
	g.Go(func() error {
		statuses, _, counts, err := workspaceDiffStats(gctx, view.root, view.rangeBefore, view.TipSHA)
		if err != nil {
			return err
		}
		summary.Files = len(statuses)
		for _, lines := range counts {
			summary.Additions += lines[0]
			summary.Deletions += lines[1]
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return WorkspaceHistory{}, err
	}
	if commits == nil {
		commits = []CommitSummary{}
	}
	return WorkspaceHistory{SessionID: id, View: view.WorkspaceViewInfo, Commits: commits, CommitsTruncated: truncated, RangeSummary: &summary}, nil
}
