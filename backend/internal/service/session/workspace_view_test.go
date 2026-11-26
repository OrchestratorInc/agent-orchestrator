package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// branchViewService serves a single-repository session checked out at repo
// whose project names main as its default branch.
func branchViewService(repo string) *Service {
	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Path: repo, Config: domain.ProjectConfig{DefaultBranch: "main"}}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "proj", Metadata: domain.SessionMetadata{Branch: "session", WorkspacePath: repo}}
	return &Service{store: st}
}

func gitHead(t *testing.T, repo string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
}

func commitWorkspaceFile(t *testing.T, repo, rel, content, message string) string {
	t.Helper()
	writeWorkspaceFile(t, repo, rel, content)
	runGit(t, repo, "add", rel)
	runGit(t, repo, "commit", "-m", message)
	return gitHead(t, repo)
}

func requireAPIError(t *testing.T, err error, kind apierr.Kind, code string) *apierr.Error {
	t.Helper()
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Kind != kind || apiError.Code != code {
		t.Fatalf("error = %#v, want %s", err, code)
	}
	return apiError
}

func TestBranchViewListsCommitsSinceDefaultMergeBase(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	base := gitHead(t, repo)
	runGit(t, repo, "switch", "-c", "feature")
	commitWorkspaceFile(t, repo, "feature.txt", "one\n", "add feature")
	tip := commitWorkspaceFile(t, repo, "feature.txt", "one\ntwo\n", "extend feature")
	runGit(t, repo, "switch", "main")
	commitWorkspaceFile(t, repo, "main.txt", "main\n", "advance main")
	runGit(t, repo, "switch", "-c", "session")
	// Staged, unstaged and untracked changes the branch view must not disturb.
	writeWorkspaceFile(t, repo, "README.md", "staged\n")
	runGit(t, repo, "add", "README.md")
	writeWorkspaceFile(t, repo, "README.md", "staged\nunstaged\n")
	writeWorkspaceFile(t, repo, "scratch.txt", "untracked\n")
	head := gitHead(t, repo)
	status := runGit(t, repo, "status", "--porcelain")
	index, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}

	history, err := branchViewService(repo).GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{Branch: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	want := WorkspaceViewInfo{Kind: WorkspaceViewBranch, Name: "feature", TipSHA: tip, BaseSHA: base, BaseRef: "main", RangeMode: WorkspaceRangeSinceBase}
	if history.View != want {
		t.Fatalf("view = %+v, want %+v", history.View, want)
	}
	if len(history.Commits) != 2 || history.Commits[0].Subject != "extend feature" || history.Commits[1].Subject != "add feature" || history.CommitsTruncated {
		t.Fatalf("commits = %+v, want the two feature commits newest first", history.Commits)
	}
	if history.RangeSummary == nil || *history.RangeSummary != (WorkspaceSummary{Files: 1, Additions: 2}) {
		t.Fatalf("range summary = %+v, want feature.txt with two additions", history.RangeSummary)
	}
	if history.Ahead != nil || history.Behind != nil {
		t.Fatalf("ahead/behind = %v/%v, want none for a branch view", history.Ahead, history.Behind)
	}

	after, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(index) {
		t.Fatal("the branch view rewrote the index")
	}
	if got := gitHead(t, repo); got != head {
		t.Fatalf("HEAD = %s, want %s", got, head)
	}
	if got := runGit(t, repo, "status", "--porcelain"); got != status {
		t.Fatalf("status = %q, want %q", got, status)
	}
}

func TestBranchViewOnDefaultBranchListsLatestFirstParentCommits(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	initial := gitHead(t, repo)
	runGit(t, repo, "switch", "-c", "session")
	fixture := make([]fixtureCommit, maxRecentCommits+10)
	for i := range fixture {
		fixture[i] = fixtureCommit{message: fmt.Sprintf("change %d", i), files: map[string]string{fmt.Sprintf("changes/%03d.txt", i): "change\n"}}
	}
	commits := importCommits(t, repo, "main", initial, fixture)
	tip := commits[len(commits)-1]

	history, err := branchViewService(repo).GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	want := WorkspaceViewInfo{Kind: WorkspaceViewBranch, Name: "main", TipSHA: tip, RangeMode: WorkspaceRangeRecent}
	if history.View != want {
		t.Fatalf("view = %+v, want %+v", history.View, want)
	}
	if len(history.Commits) != maxRecentCommits || !history.CommitsTruncated || history.Commits[0].SHA != tip {
		t.Fatalf("listed %d commits, truncated=%v; want the newest %d and truncated", len(history.Commits), history.CommitsTruncated, maxRecentCommits)
	}
	oldest := history.Commits[len(history.Commits)-1].SHA
	var wantSummary WorkspaceSummary
	for _, line := range strings.Split(strings.TrimSpace(runGit(t, repo, "diff", "--numstat", oldest+"^", tip)), "\n") {
		var additions, deletions int
		var path string
		if _, err := fmt.Sscanf(line, "%d\t%d\t%s", &additions, &deletions, &path); err != nil {
			t.Fatalf("numstat line %q: %v", line, err)
		}
		wantSummary.Files++
		wantSummary.Additions += additions
		wantSummary.Deletions += deletions
	}
	if history.RangeSummary == nil || *history.RangeSummary != wantSummary || wantSummary.Files != maxRecentCommits {
		t.Fatalf("range summary = %+v, want %+v from the oldest listed commit's parent to the tip", history.RangeSummary, wantSummary)
	}
}

func TestBranchViewWithoutDefaultBranchStartsAtTheRootCommit(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "switch", "-c", "feature")
	tip := commitWorkspaceFile(t, repo, "feature.txt", "feature\n", "add feature")
	runGit(t, repo, "switch", "main")
	// No project and no remote: nothing names a default branch to measure from.
	svc := workspaceReviewService(t, repo)

	history, err := svc.GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{Branch: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if history.View.RangeMode != WorkspaceRangeRecent || history.View.TipSHA != tip || history.View.BaseSHA != "" {
		t.Fatalf("view = %+v, want recent mode at the feature tip", history.View)
	}
	if len(history.Commits) != 2 || history.CommitsTruncated {
		t.Fatalf("commits = %+v, want the whole two-commit history", history.Commits)
	}
	// The range starts from the empty tree, so the root commit's files count.
	if history.RangeSummary == nil || *history.RangeSummary != (WorkspaceSummary{Files: 4, Additions: 4}) {
		t.Fatalf("range summary = %+v, want every file from the root commit on", history.RangeSummary)
	}
}

func TestBranchViewRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"main", "feature/login", "ao/session-1", "v1.2", "a@b"} {
		if !validWorkspaceBranchName(name) {
			t.Errorf("validWorkspaceBranchName(%q) = false, want true", name)
		}
	}
	invalid := []string{"-x", "a..b", "x*", "@{-1}", "x.lock", "", "@", " main", "a b", "/a", "a/", "a//b", "a.", ".a", "a/.b", "a/b.lock", "a~1", "a^", "a:b", "a?", "a[b", `a\b`, "a\x01", strings.Repeat("a", 256)}
	for _, name := range invalid {
		if validWorkspaceBranchName(name) {
			t.Errorf("validWorkspaceBranchName(%q) = true, want false", name)
		}
	}

	svc := branchViewService(newWorkspaceRepo(t))
	for _, name := range []string{"-x", "a..b", "x*", "@{-1}", "x.lock", " "} {
		_, err := svc.GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{Branch: name})
		requireAPIError(t, err, apierr.KindInvalid, "INVALID_WORKSPACE_BRANCH")
	}
}

func TestWorkspaceViewRejectsAmbiguousSelectors(t *testing.T) {
	svc := branchViewService(newWorkspaceRepo(t))
	tests := []struct {
		view WorkspaceView
		code string
	}{
		{WorkspaceView{Branch: "main", PRNumber: 7}, "WORKSPACE_VIEW_AMBIGUOUS"},
		{WorkspaceView{SourceURL: "https://github.com/acme/repo/pull/7"}, "INVALID_WORKSPACE_VIEW"},
		{WorkspaceView{PRNumber: -1}, "INVALID_WORKSPACE_VIEW"},
	}
	for _, tc := range tests {
		_, err := svc.GetWorkspaceHistory(context.Background(), "ao-1", tc.view)
		requireAPIError(t, err, apierr.KindInvalid, tc.code)
	}
}

func TestBranchViewUnknownBranchIsNotFound(t *testing.T) {
	svc := branchViewService(newWorkspaceRepo(t))
	_, err := svc.GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{Branch: "missing"})
	requireAPIError(t, err, apierr.KindNotFound, "WORKSPACE_BRANCH_NOT_FOUND")
}

func TestBranchViewIsUnsupportedOutsideSingleRepositories(t *testing.T) {
	for _, kind := range []domain.ProjectKind{domain.ProjectKindWorkspace, domain.ProjectKindScratch} {
		repo := newWorkspaceRepo(t)
		st := newFakeStore()
		st.projects["proj"] = domain.ProjectRecord{ID: "proj", Path: repo, Kind: kind}
		st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "proj", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
		_, err := (&Service{store: st}).GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{Branch: "main"})
		requireAPIError(t, err, apierr.KindInvalid, "WORKSPACE_VIEW_UNSUPPORTED")
	}
}

func TestBranchViewRejectsMovedTip(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	base := gitHead(t, repo)
	runGit(t, repo, "switch", "-c", "feature")
	listed := commitWorkspaceFile(t, repo, "feature.txt", "one\n", "add feature")
	moved := commitWorkspaceFile(t, repo, "feature.txt", "one\ntwo\n", "extend feature")
	runGit(t, repo, "switch", "main")
	svc := branchViewService(repo)

	_, err := svc.GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{Branch: "feature", ExpectedTipSHA: listed, ExpectedBaseSHA: base})
	apiError := requireAPIError(t, err, apierr.KindConflict, "WORKSPACE_REF_MOVED")
	if apiError.Details["tipSha"] != moved || apiError.Details["baseSha"] != base {
		t.Fatalf("details = %#v, want the current tip and base", apiError.Details)
	}
	_, err = svc.GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{Branch: "feature", ExpectedTipSHA: moved, ExpectedBaseSHA: listed})
	requireAPIError(t, err, apierr.KindConflict, "WORKSPACE_REF_MOVED")
	if _, err := svc.GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{Branch: "feature", ExpectedTipSHA: moved, ExpectedBaseSHA: base}); err != nil {
		t.Fatalf("a current fence = %v, want it accepted", err)
	}
}

func TestPRViewReadsFetchedHeadWithoutTouchingWorktree(t *testing.T) {
	remoteRoot := t.TempDir()
	upstream := filepath.Join(remoteRoot, "upstream.git")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, upstream, "init", "--bare")

	source := newWorkspaceRepo(t)
	runGit(t, source, "branch", "-M", "main")
	forkPoint := gitHead(t, source)
	runGit(t, source, "switch", "-c", "feature")
	head := commitWorkspaceFile(t, source, "feature.txt", "feature\n", "feature")
	runGit(t, source, "switch", "main")
	base := commitWorkspaceFile(t, source, "base.txt", "base advanced\n", "advance base")
	runGit(t, source, "remote", "add", "publish", upstream)
	runGit(t, source, "push", "publish", "main:refs/heads/main", head+":refs/pull/42/head")

	checkout := filepath.Join(remoteRoot, "checkout")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, checkout, "init")
	runGit(t, checkout, "remote", "add", "origin", "https://github.com/acme/repo.git")
	runGit(t, checkout, "config", "url.file://"+upstream+".insteadOf", "https://github.com/acme/repo.git")
	writeWorkspaceFile(t, checkout, "local.txt", "local work\n")
	symbolicHead := runGit(t, checkout, "symbolic-ref", "HEAD")
	status := runGit(t, checkout, "status", "--porcelain")

	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: checkout}}
	st.prs["ao-1"] = []domain.PullRequest{{Number: 42, URL: "https://github.com/acme/repo/pull/42", Provider: "github", Host: "github.com", Repo: "acme/repo", SourceBranch: "feature", TargetBranch: "main", BaseSHA: base, HeadSHA: head}}

	history, err := (&Service{store: st}).GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{PRNumber: 42, SourceURL: "https://github.com/acme/repo/pull/42"})
	if err != nil {
		t.Fatal(err)
	}
	want := WorkspaceViewInfo{Kind: WorkspaceViewPullRequest, Name: "feature", TipSHA: head, BaseSHA: forkPoint, BaseRef: "main", RangeMode: WorkspaceRangeSinceBase}
	if history.View != want {
		t.Fatalf("view = %+v, want %+v", history.View, want)
	}
	if len(history.Commits) != 1 || history.Commits[0].SHA != head {
		t.Fatalf("commits = %+v, want the PR's one commit", history.Commits)
	}
	if history.RangeSummary == nil || *history.RangeSummary != (WorkspaceSummary{Files: 1, Additions: 1}) {
		t.Fatalf("range summary = %+v, want feature.txt only", history.RangeSummary)
	}
	if got := runGit(t, checkout, "symbolic-ref", "HEAD"); got != symbolicHead {
		t.Fatalf("HEAD = %q, want %q", got, symbolicHead)
	}
	if got := runGit(t, checkout, "status", "--porcelain"); got != status {
		t.Fatalf("status = %q, want %q", got, status)
	}
}

func TestBranchViewCommitListDoesNotReadWorktreeSizes(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "switch", "-c", "feature")
	commitWorkspaceFile(t, repo, "data.txt", "branch text\n", "add data")
	runGit(t, repo, "switch", "main")
	// The checked-out worktree has an unrelated binary file at the same path.
	writeWorkspaceFile(t, repo, "data.txt", "worktree\x00binary\x00"+strings.Repeat("x", 4096))

	history, err := branchViewService(repo).GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{Branch: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Commits) != 1 || len(history.Commits[0].Files) != 1 {
		t.Fatalf("commits = %+v, want one commit changing data.txt", history.Commits)
	}
	file := history.Commits[0].Files[0]
	if file.Path != "data.txt" || file.Size != 0 || file.Binary || file.Additions != 1 {
		t.Fatalf("file = %+v, want the branch's text file with no worktree size", file)
	}
}
