package session

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const maxWorkspaceBranches = 200

type WorkspaceBranchKind string

const (
	WorkspaceBranchRemote      WorkspaceBranchKind = "remote"
	WorkspaceBranchLocal       WorkspaceBranchKind = "local"
	WorkspaceBranchPullRequest WorkspaceBranchKind = "pull_request"
)

type WorkspaceBranch struct {
	Kind       WorkspaceBranchKind
	Name       string
	TipSHA     string
	UpdatedAt  time.Time
	CheckedOut bool
	Detached   bool
	IsDefault  bool
	PRNumber   int
	PRURL      string
	PRTitle    string
}

type WorkspaceBranches struct {
	SessionID     domain.SessionID
	Supported     bool
	DefaultBranch string
	Branches      []WorkspaceBranch
	Truncated     bool
}

func (s *Service) ListWorkspaceBranches(ctx context.Context, id domain.SessionID) (WorkspaceBranches, error) {
	rec, err := s.sessionWorkspaceRecord(ctx, id)
	if err != nil {
		return WorkspaceBranches{}, err
	}
	project, projectOK, err := s.sessionProject(ctx, rec)
	if err != nil {
		return WorkspaceBranches{}, err
	}
	result := WorkspaceBranches{SessionID: id, Branches: []WorkspaceBranch{}}
	if isStandaloneScratchWorkspace(rec) || (projectOK && project.Kind.WithDefault() == domain.ProjectKindScratch) {
		return result, nil
	}
	result.Supported = true
	prs, err := s.readableWorkspacePRs(ctx, id)
	if err != nil {
		return WorkspaceBranches{}, err
	}
	root := rec.Metadata.WorkspacePath
	checkedOut := WorkspaceBranch{Kind: WorkspaceBranchLocal, Name: rec.Metadata.Branch, CheckedOut: true}
	var locals []WorkspaceBranch
	var localsTruncated bool
	var detachedAt time.Time
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		result.DefaultBranch, _, _ = viewDefaultBranchName(gctx, root, project, projectOK)
		return nil
	})
	if !projectOK || project.Kind.WithDefault() == domain.ProjectKindSingleRepo {
		if _, branch, head, ok := readGitHead(root); ok {
			checkedOut.Name, checkedOut.TipSHA, checkedOut.Detached = branch, head, branch == ""
		}
		if checkedOut.Detached {
			g.Go(func() error {
				detachedAt = gitCommitTime(gctx, root, checkedOut.TipSHA)
				return nil
			})
		}
		g.Go(func() (err error) {
			locals, localsTruncated, err = listLocalBranches(gctx, root, checkedOut.Name, prs)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return WorkspaceBranches{}, err
	}
	checkedOut.UpdatedAt = detachedAt
	owners := prBranchOwners(ctx, root, checkedOut, locals, prs)
	result.Branches, result.Truncated = workspaceBranchRows(checkedOut, locals, prs, owners, result.DefaultBranch)
	result.Truncated = result.Truncated || localsTruncated
	return result, nil
}

func prBranchOwners(ctx context.Context, root string, checkedOut WorkspaceBranch, locals []WorkspaceBranch, prs []domain.PullRequest) map[string]bool {
	owners := map[string]bool{}
	if len(prs) == 0 {
		return owners
	}
	remotes := gitRemoteURLs(ctx, root)
	tips := make(map[string]string, len(locals)+1)
	for _, row := range locals {
		if row.Kind != WorkspaceBranchLocal {
			continue
		}
		tips[row.Name] = row.TipSHA
	}
	if !checkedOut.Detached && checkedOut.TipSHA != "" {
		tips[checkedOut.Name] = checkedOut.TipSHA
	}
	var pending []domain.PullRequest
	for _, pr := range prs {
		if requireSameRepo(pr.URL, remotes["origin"]) != nil {
			continue
		}
		tip, ok := tips[pr.SourceBranch]
		switch {
		case !ok || pr.SourceBranch == "":
		case tip == pr.HeadSHA:
			owners[pr.URL] = true
		case validWorkspaceBranchName(pr.SourceBranch):
			pending = append(pending, pr)
		}
	}
	if len(pending) == 0 {
		return owners
	}
	var patterns []string
	for _, pr := range pending {
		for name, url := range remotes {
			if requireSameRepo(pr.URL, url) == nil {
				patterns = append(patterns, "refs/remotes/"+name+"/"+pr.SourceBranch)
			}
		}
	}
	if len(patterns) == 0 {
		return owners
	}
	out, err := gitWorkspaceOutput(ctx, root, append([]string{"for-each-ref", "--format=%(refname)%00%(objectname)"}, patterns...)...)
	if err != nil {
		return owners
	}
	remoteTips := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if ref, sha, ok := strings.Cut(line, "\x00"); ok {
			remoteTips[ref] = sha
		}
	}
	for _, pr := range pending {
		for name, url := range remotes {
			if requireSameRepo(pr.URL, url) == nil && remoteTips["refs/remotes/"+name+"/"+pr.SourceBranch] == pr.HeadSHA {
				owners[pr.URL] = true
			}
		}
	}
	return owners
}

func gitRemoteURLs(ctx context.Context, root string) map[string]string {
	out, err := gitWorkspaceOutput(ctx, root, "config", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		return nil
	}
	remotes := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, url, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if name, ok := strings.CutSuffix(strings.TrimPrefix(key, "remote."), ".url"); ok && name != "" {
			remotes[name] = url
		}
	}
	return remotes
}

func (s *Service) readableWorkspacePRs(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list PRs for branches: %w", err)
	}
	var readable []domain.PullRequest
	for _, group := range groupPullRequestAliases(prs) {
		pr := group.primary
		if pr.Number > 0 && strings.TrimSpace(pr.BaseSHA) != "" && strings.TrimSpace(pr.HeadSHA) != "" {
			readable = append(readable, pr)
		}
	}
	sort.SliceStable(readable, func(i, j int) bool {
		return pullRequestUpdatedAt(readable[i]).After(pullRequestUpdatedAt(readable[j]))
	})
	return readable, nil
}

func pullRequestUpdatedAt(pr domain.PullRequest) time.Time {
	if !pr.UpdatedAtProvider.IsZero() {
		return pr.UpdatedAtProvider
	}
	return pr.UpdatedAt
}

func listLocalBranches(ctx context.Context, root, checkedOut string, prs []domain.PullRequest) ([]WorkspaceBranch, bool, error) {
	locals, truncated, err := readLocalBranches(ctx, root, "--sort=-committerdate", "--count="+strconv.Itoa(maxWorkspaceBranches+1), "refs/heads/", "refs/remotes/")
	if err != nil || !truncated {
		return locals, truncated, err
	}
	listed := make(map[string]bool, len(locals))
	for _, row := range locals {
		if row.Kind != WorkspaceBranchLocal {
			continue
		}
		listed[row.Name] = true
	}
	names := []string{checkedOut}
	for _, pr := range prs {
		names = append(names, pr.SourceBranch)
	}
	var patterns []string
	want := map[string]bool{}
	for _, name := range names {
		if !listed[name] && !want[name] && validWorkspaceBranchName(name) {
			want[name] = true
			patterns = append(patterns, "refs/heads/"+name)
		}
	}
	if len(patterns) == 0 {
		return locals, truncated, nil
	}
	found, _, err := readLocalBranches(ctx, root, patterns...)
	if err != nil {
		return nil, false, err
	}
	for _, row := range found {
		if want[row.Name] {
			locals = append(locals, row)
		}
	}
	return locals, truncated, nil
}

func readLocalBranches(ctx context.Context, root string, args ...string) ([]WorkspaceBranch, bool, error) {
	out, err := gitWorkspaceOutput(ctx, root, append([]string{"for-each-ref", "--format=%(refname)%00%(objectname)%00%(committerdate:unix)"}, args...)...)
	if err != nil {
		return nil, false, err
	}
	var rows []WorkspaceBranch
	rawCount := 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\x00")
		if len(fields) != 3 {
			continue
		}
		rawCount++
		kind := WorkspaceBranchLocal
		name, ok := strings.CutPrefix(fields[0], "refs/heads/")
		if !ok {
			name, ok = strings.CutPrefix(fields[0], "refs/remotes/")
			kind = WorkspaceBranchRemote
			if strings.HasSuffix(name, "/HEAD") {
				continue
			}
		}
		if !ok || name == "" {
			continue
		}
		rows = append(rows, WorkspaceBranch{Kind: kind, Name: name, TipSHA: fields[1], UpdatedAt: unixTime(fields[2])})
	}
	limit := 0
	for _, arg := range args {
		if raw, ok := strings.CutPrefix(arg, "--count="); ok {
			limit, _ = strconv.Atoi(raw)
		}
	}
	return rows, limit > 0 && rawCount >= limit, nil
}

func gitCommitTime(ctx context.Context, root, sha string) time.Time {
	if !fullCommitSHA.MatchString(sha) {
		return time.Time{}
	}
	out, err := gitWorkspaceOutput(ctx, root, "log", "-1", "--format=%ct", sha, "--")
	if err != nil {
		return time.Time{}
	}
	return unixTime(out)
}

func unixTime(raw string) time.Time {
	seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

func workspaceBranchRows(checkedOut WorkspaceBranch, locals []WorkspaceBranch, prs []domain.PullRequest, owners map[string]bool, defaultBranch string) ([]WorkspaceBranch, bool) {
	rows := make([]WorkspaceBranch, 0, 1+len(locals)+len(prs))
	rows = append(rows, checkedOut)
	byName := map[string]int{}
	if !checkedOut.Detached {
		byName[checkedOut.Name] = 0
	}
	for _, row := range locals {
		if row.Kind == WorkspaceBranchRemote {
			rows = append(rows, row)
			continue
		}
		if _, ok := byName[row.Name]; ok {
			if row.Name == checkedOut.Name {
				rows[0].TipSHA, rows[0].UpdatedAt = row.TipSHA, row.UpdatedAt
			}
			continue
		}
		byName[row.Name] = len(rows)
		rows = append(rows, row)
	}
	for _, pr := range prs {
		if i, ok := byName[pr.SourceBranch]; ok && owners[pr.URL] && rows[i].PRNumber == 0 {
			rows[i].PRNumber, rows[i].PRURL, rows[i].PRTitle = pr.Number, pr.URL, pr.Title
			continue
		}
		rows = append(rows, WorkspaceBranch{
			Kind: WorkspaceBranchPullRequest, Name: pr.SourceBranch, TipSHA: pr.HeadSHA, UpdatedAt: pullRequestUpdatedAt(pr),
			PRNumber: pr.Number, PRURL: pr.URL, PRTitle: pr.Title,
		})
	}
	for i := range rows {
		rows[i].IsDefault = defaultBranch != "" && rows[i].Kind == WorkspaceBranchLocal && !rows[i].Detached && rows[i].Name == defaultBranch
	}
	others := rows[1:]
	sort.SliceStable(others, func(i, j int) bool { return newerWorkspaceBranch(others[i], others[j]) })
	if len(rows) > maxWorkspaceBranches {
		return rows[:maxWorkspaceBranches], true
	}
	return rows, false
}

func newerWorkspaceBranch(a, b WorkspaceBranch) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	if a.Kind != b.Kind {
		return a.Kind == WorkspaceBranchLocal
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.PRNumber < b.PRNumber
}
