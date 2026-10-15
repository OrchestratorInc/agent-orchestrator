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

// maxWorkspaceBranches caps the branch list, checked-out row included.
const maxWorkspaceBranches = 200

// WorkspaceBranchKind says how a listed branch is read.
type WorkspaceBranchKind string

const (
	// WorkspaceBranchLocal is a local branch, read as a branch view (or as the
	// checked-out worktree when it is checked out).
	WorkspaceBranchLocal WorkspaceBranchKind = "local"
	// WorkspaceBranchPullRequest is a pull request whose source branch is not
	// local, read as a pull request view.
	WorkspaceBranchPullRequest WorkspaceBranchKind = "pull_request"
)

// WorkspaceBranch is one row of the branch list. A local row whose branch has
// a pull request carries that PR's number, URL and title; only a PR with no
// local branch gets a row of its own.
type WorkspaceBranch struct {
	Kind WorkspaceBranchKind
	// Name is the branch, the PR's source branch, or empty for a detached HEAD.
	Name      string
	TipSHA    string
	UpdatedAt time.Time
	// CheckedOut marks the session worktree's own row, which is always first.
	CheckedOut bool
	Detached   bool
	IsDefault  bool
	PRNumber   int
	PRURL      string
	PRTitle    string
}

// WorkspaceBranches lists the branches a session's review can read.
// Supported is false for scratch sessions, which have no Git history.
type WorkspaceBranches struct {
	SessionID     domain.SessionID
	Supported     bool
	DefaultBranch string
	Branches      []WorkspaceBranch
	Truncated     bool
}

// ListWorkspaceBranches lists the checked-out branch first, then the other
// local branches and the session's pull requests whose source branch is not
// local, newest first, capped at maxWorkspaceBranches. A workspace project
// lists only its checked-out branch and its pull requests, since only a
// single repository can review another branch. Everything is read, nothing
// is checked out.
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
	var detachedAt time.Time
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		result.DefaultBranch, _, _ = viewDefaultBranchName(gctx, root, project, projectOK)
		return nil
	})
	if !projectOK || project.Kind.WithDefault() == domain.ProjectKindSingleRepo {
		// HEAD is read from the files git reads; the recorded branch stands in
		// only when they cannot be read.
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
			locals, err = listLocalBranches(gctx, root, checkedOut.Name, prs)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return WorkspaceBranches{}, err
	}
	checkedOut.UpdatedAt = detachedAt
	result.Branches, result.Truncated = workspaceBranchRows(checkedOut, locals, prs, result.DefaultBranch)
	return result, nil
}

// readableWorkspacePRs returns the session's pull requests a pull request view
// can read, one per alias group, most recently updated first.
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

// listLocalBranches lists the newest local branches, one more than the cap so
// a longer list is known to be truncated. When it is, the checked-out branch
// and the PR source branches that were left out are looked up by exact name
// in one more process, so a local PR branch never gets a pull request row.
func listLocalBranches(ctx context.Context, root, checkedOut string, prs []domain.PullRequest) ([]WorkspaceBranch, error) {
	locals, err := readLocalBranches(ctx, root, "--sort=-committerdate", "--count="+strconv.Itoa(maxWorkspaceBranches+1), "refs/heads/")
	if err != nil || len(locals) <= maxWorkspaceBranches {
		return locals, err
	}
	listed := make(map[string]bool, len(locals))
	for _, row := range locals {
		listed[row.Name] = true
	}
	names := []string{checkedOut}
	for _, pr := range prs {
		names = append(names, pr.SourceBranch)
	}
	var patterns []string
	want := map[string]bool{}
	for _, name := range names {
		// A valid branch name has no glob characters, so the pattern matches
		// it literally (and its children, which are filtered out below).
		if !listed[name] && !want[name] && validWorkspaceBranchName(name) {
			want[name] = true
			patterns = append(patterns, "refs/heads/"+name)
		}
	}
	if len(patterns) == 0 {
		return locals, nil
	}
	found, err := readLocalBranches(ctx, root, patterns...)
	if err != nil {
		return nil, err
	}
	for _, row := range found {
		if want[row.Name] {
			locals = append(locals, row)
		}
	}
	return locals, nil
}

// readLocalBranches runs for-each-ref over refs/heads with args and parses
// each branch's name, tip and committer time.
func readLocalBranches(ctx context.Context, root string, args ...string) ([]WorkspaceBranch, error) {
	out, err := gitWorkspaceOutput(ctx, root, append([]string{"for-each-ref", "--format=%(refname)%00%(objectname)%00%(committerdate:unix)"}, args...)...)
	if err != nil {
		return nil, err
	}
	var rows []WorkspaceBranch
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\x00")
		if len(fields) != 3 {
			continue
		}
		name, ok := strings.CutPrefix(fields[0], "refs/heads/")
		if !ok || name == "" {
			continue
		}
		rows = append(rows, WorkspaceBranch{Kind: WorkspaceBranchLocal, Name: name, TipSHA: fields[1], UpdatedAt: unixTime(fields[2])})
	}
	return rows, nil
}

// gitCommitTime is a commit's committer time, or zero when it cannot be read.
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

// workspaceBranchRows puts the checked-out row first and the rest newest
// first. A pull request whose source branch has a row annotates it (the most
// recently updated PR wins); any other gets a row of its own. The cap always
// keeps the checked-out row.
func workspaceBranchRows(checkedOut WorkspaceBranch, locals []WorkspaceBranch, prs []domain.PullRequest, defaultBranch string) ([]WorkspaceBranch, bool) {
	rows := make([]WorkspaceBranch, 0, 1+len(locals)+len(prs))
	rows = append(rows, checkedOut)
	byName := map[string]int{}
	if !checkedOut.Detached {
		byName[checkedOut.Name] = 0
	}
	for _, row := range locals {
		if _, ok := byName[row.Name]; ok {
			// The checked-out branch keeps its first row, now with its tip.
			if row.Name == checkedOut.Name {
				rows[0].TipSHA, rows[0].UpdatedAt = row.TipSHA, row.UpdatedAt
			}
			continue
		}
		byName[row.Name] = len(rows)
		rows = append(rows, row)
	}
	for _, pr := range prs {
		if i, ok := byName[pr.SourceBranch]; ok && pr.SourceBranch != "" {
			if rows[i].PRNumber == 0 {
				rows[i].PRNumber, rows[i].PRURL, rows[i].PRTitle = pr.Number, pr.URL, pr.Title
			}
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
