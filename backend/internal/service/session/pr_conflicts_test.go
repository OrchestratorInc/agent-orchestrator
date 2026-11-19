package session

import (
	"context"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

// conflictRepo returns a repo with main pushed to origin and a feature branch
// whose x.txt and y.txt differ from main's, plus the feature branch's tip.
func conflictRepo(t *testing.T, conflicting ...string) (repo, headSHA string) {
	t.Helper()
	repo = newPushableRepo(t)
	for _, name := range conflicting {
		writeWorkspaceFile(t, repo, name, "base\n")
		runGit(t, repo, "add", name)
	}
	runGit(t, repo, "commit", "-m", "add shared files")
	runGit(t, repo, "push", "origin", "main")
	runGit(t, repo, "checkout", "-b", "feat/x")
	for _, name := range conflicting {
		writeWorkspaceFile(t, repo, name, "branch side\n")
		runGit(t, repo, "add", name)
	}
	runGit(t, repo, "commit", "-m", "branch edits")
	headSHA = strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "checkout", "main")
	for _, name := range conflicting {
		writeWorkspaceFile(t, repo, name, "main side\n")
		runGit(t, repo, "add", name)
	}
	runGit(t, repo, "commit", "-m", "main edits")
	runGit(t, repo, "push", "origin", "main")
	runGit(t, repo, "checkout", "feat/x")
	return repo, headSHA
}

func TestPRConflictFilesNamesTheConflictingFilesSorted(t *testing.T) {
	repo, head := conflictRepo(t, "y.txt", "x.txt")
	svc := &Service{}
	got := svc.prConflictFiles(context.Background(), repo, domain.PullRequest{HeadSHA: head, TargetBranch: "main"})
	if want := []string{"x.txt", "y.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("conflict files = %v, want %v", got, want)
	}
}

func TestPRConflictFilesIsEmptyForACleanMerge(t *testing.T) {
	repo := newPushableRepo(t)
	runGit(t, repo, "checkout", "-b", "feat/x")
	commitFile(t, repo, "own.go")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "checkout", "main")
	commitFile(t, repo, "theirs.go")
	runGit(t, repo, "push", "origin", "main")
	runGit(t, repo, "checkout", "feat/x")

	got := (&Service{}).prConflictFiles(context.Background(), repo, domain.PullRequest{HeadSHA: head, TargetBranch: "main"})
	if len(got) != 0 {
		t.Fatalf("conflict files = %v, want none for a clean merge", got)
	}
}

// An answer that cannot be computed is "unknown", which must never read as a
// list of files.
func TestPRConflictFilesIsUnknownWhenACommitIsNotAvailableLocally(t *testing.T) {
	repo, head := conflictRepo(t, "x.txt")
	svc := &Service{}
	ctx := context.Background()
	for name, pr := range map[string]domain.PullRequest{
		"no head sha":    {TargetBranch: "main"},
		"head not local": {HeadSHA: strings.Repeat("a", 40), TargetBranch: "main"},
		"base not found": {HeadSHA: head, TargetBranch: "no-such-branch"},
	} {
		if got := svc.prConflictFiles(ctx, repo, pr); got != nil {
			t.Errorf("%s: conflict files = %v, want nil (unknown)", name, got)
		}
	}
	if got := svc.prConflictFiles(ctx, "", domain.PullRequest{HeadSHA: head, TargetBranch: "main"}); got != nil {
		t.Errorf("no workspace: conflict files = %v, want nil", got)
	}
}

func TestPRConflictFilesMergesOnlyOncePerPairOfCommits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("counts git through a shell wrapper")
	}
	repo, head := conflictRepo(t, "x.txt")
	svc := &Service{}
	pr := domain.PullRequest{HeadSHA: head, TargetBranch: "main"}
	calls := countGitCalls(t)

	svc.prConflictFiles(context.Background(), repo, pr)
	first := calls()
	svc.prConflictFiles(context.Background(), repo, pr)
	second := calls() - first
	if second >= first {
		t.Fatalf("a repeat ran %d git processes, the first ran %d: the merge was not cached", second, first)
	}

	// A moved base is a new pair of commits and must be computed again.
	runGit(t, repo, "checkout", "main")
	commitFile(t, repo, "later.go")
	runGit(t, repo, "push", "origin", "main")
	runGit(t, repo, "checkout", "feat/x")
	before := calls()
	svc.prConflictFiles(context.Background(), repo, pr)
	if calls()-before < first {
		t.Fatalf("a moved base ran %d git processes, want a fresh merge (about %d)", calls()-before, first)
	}
}

func TestListPRSummariesNamesConflictFilesOnlyForAnOpenConflictingPR(t *testing.T) {
	repo, head := conflictRepo(t, "x.txt")
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	conflicting := domain.PullRequest{URL: "https://github.com/acme/repo/pull/1", SessionID: "mer-1", Number: 1, HeadSHA: head, TargetBranch: "main", Mergeability: domain.MergeConflicting}
	merged := domain.PullRequest{URL: "https://github.com/acme/repo/pull/2", SessionID: "mer-1", Number: 2, HeadSHA: head, TargetBranch: "main", Mergeability: domain.MergeConflicting, Merged: true}
	clean := domain.PullRequest{URL: "https://github.com/acme/repo/pull/3", SessionID: "mer-1", Number: 3, HeadSHA: head, TargetBranch: "main", Mergeability: domain.MergeMergeable}
	svc := &Service{store: &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{conflicting, merged, clean}}}

	got, err := svc.ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	byNumber := map[int]PRSummary{}
	for _, pr := range got {
		byNumber[pr.Number] = pr
	}
	if files := byNumber[1].Mergeability.ConflictFiles; len(files) != 1 || files[0] != (contract.PullRequestConflictFile{Path: "x.txt"}) {
		t.Fatalf("open conflicting PR conflict files = %+v, want x.txt", files)
	}
	if files := byNumber[2].Mergeability.ConflictFiles; len(files) != 0 {
		t.Fatalf("merged PR conflict files = %+v, want none", files)
	}
	if files := byNumber[3].Mergeability.ConflictFiles; len(files) != 0 {
		t.Fatalf("mergeable PR conflict files = %+v, want none", files)
	}
}
