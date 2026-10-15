package session

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// importBranches adds one empty commit on top of parent to each named branch,
// committed a second apart from start in the order given, in one git
// fast-import.
func importBranches(t *testing.T, repo, parent string, start int64, names ...string) {
	t.Helper()
	var stream strings.Builder
	for i, name := range names {
		message := "update " + name
		fmt.Fprintf(&stream, "commit refs/heads/%s\ncommitter AO Tests <ao@example.com> %d +0000\ndata %d\n%s\nfrom %s\n", name, start+int64(i), len(message), message, parent)
	}
	cmd := exec.Command("git", "-C", repo, "fast-import", "--quiet")
	cmd.Stdin = strings.NewReader(stream.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import: %v\n%s", err, out)
	}
}

func branchNames(rows []WorkspaceBranch) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	return names
}

func readablePR(number int, source, head string, updated time.Time) domain.PullRequest {
	return domain.PullRequest{
		URL: fmt.Sprintf("https://github.com/acme/repo/pull/%d", number), Number: number, Title: fmt.Sprintf("PR %d", number),
		SourceBranch: source, TargetBranch: "main", BaseSHA: strings.Repeat("b", 40), HeadSHA: head, UpdatedAt: updated,
	}
}

func TestListWorkspaceBranchesPutsCheckedOutFirstThenNewest(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	importBranches(t, repo, gitHead(t, repo), 1600000000, "session", "older", "newer")
	runGit(t, repo, "switch", "session")
	main := strings.TrimSpace(runGit(t, repo, "rev-parse", "main"))

	branches, err := branchViewService(repo).ListWorkspaceBranches(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if !branches.Supported || branches.DefaultBranch != "main" || branches.Truncated {
		t.Fatalf("branches = %+v, want a supported, complete list with default main", branches)
	}
	if got, want := strings.Join(branchNames(branches.Branches), ","), "session,main,newer,older"; got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}
	session := branches.Branches[0]
	if !session.CheckedOut || session.Kind != WorkspaceBranchLocal || session.TipSHA != gitHead(t, repo) || !session.UpdatedAt.Equal(time.Unix(1600000000, 0)) || session.IsDefault {
		t.Fatalf("checked-out row = %+v", session)
	}
	if row := branches.Branches[1]; row.CheckedOut || !row.IsDefault || row.TipSHA != main {
		t.Fatalf("main row = %+v, want the default branch at %s", row, main)
	}
	if row := branches.Branches[2]; !row.UpdatedAt.Equal(time.Unix(1600000002, 0)) || row.IsDefault {
		t.Fatalf("newer row = %+v", row)
	}
}

func TestListWorkspaceBranchesCapsAt200KeepingCheckedOut(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	names := []string{"session"}
	for i := 0; i < maxWorkspaceBranches+5; i++ {
		names = append(names, fmt.Sprintf("b%03d", i))
	}
	// The checked-out branch is the oldest, so the newest-first listing
	// leaves it out and it has to be looked up by name.
	importBranches(t, repo, gitHead(t, repo), 1600000000, names...)
	runGit(t, repo, "switch", "session")
	svc := branchViewService(repo)
	store := svc.store.(*fakeStore)
	// b000 is local but left out too, so its PR must not become a PR row.
	store.prs["ao-1"] = []domain.PullRequest{readablePR(9, "b000", strings.Repeat("c", 40), time.Now())}

	branches, err := svc.ListWorkspaceBranches(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(branches.Branches) != maxWorkspaceBranches || !branches.Truncated {
		t.Fatalf("listed %d rows, truncated=%v; want %d and truncated", len(branches.Branches), branches.Truncated, maxWorkspaceBranches)
	}
	session := branches.Branches[0]
	if session.Name != "session" || !session.CheckedOut || session.TipSHA != gitHead(t, repo) || !session.UpdatedAt.Equal(time.Unix(1600000000, 0)) {
		t.Fatalf("first row = %+v, want the checked-out branch with its tip", session)
	}
	if first, last := branches.Branches[1].Name, branches.Branches[len(branches.Branches)-1].Name; first != "main" || last != "b007" {
		t.Fatalf("rows run %s..%s, want main..b007", first, last)
	}
	for _, row := range branches.Branches {
		if row.Kind != WorkspaceBranchLocal {
			t.Fatalf("row %+v, want no pull request row for the local b000", row)
		}
	}
}

func TestListWorkspaceBranchesAnnotatesLocalPRAndAddsNonLocalPR(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	importBranches(t, repo, gitHead(t, repo), 1600000000, "session", "feature")
	runGit(t, repo, "switch", "session")
	svc := branchViewService(repo)
	remoteHead := strings.Repeat("d", 40)
	remote := readablePR(2, "remote-only", remoteHead, time.Unix(1500000000, 0))
	remote.UpdatedAtProvider = time.Unix(1650000000, 0).UTC()
	unreadable := readablePR(4, "unreadable", strings.Repeat("e", 40), time.Now())
	unreadable.BaseSHA = ""
	svc.store.(*fakeStore).prs["ao-1"] = []domain.PullRequest{
		readablePR(1, "session", gitHead(t, repo), time.Now()),
		remote,
		readablePR(3, "feature", strings.Repeat("f", 40), time.Now()),
		unreadable,
	}

	branches, err := svc.ListWorkspaceBranches(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(branchNames(branches.Branches), ","), "session,main,remote-only,feature"; got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}
	if row := branches.Branches[0]; row.Kind != WorkspaceBranchLocal || row.PRNumber != 1 || row.PRTitle != "PR 1" || row.PRURL != "https://github.com/acme/repo/pull/1" {
		t.Fatalf("checked-out row = %+v, want it annotated with PR 1", row)
	}
	if row := branches.Branches[3]; row.Kind != WorkspaceBranchLocal || row.PRNumber != 3 {
		t.Fatalf("feature row = %+v, want it annotated with PR 3", row)
	}
	want := WorkspaceBranch{
		Kind: WorkspaceBranchPullRequest, Name: "remote-only", TipSHA: remoteHead, UpdatedAt: remote.UpdatedAtProvider,
		PRNumber: 2, PRURL: "https://github.com/acme/repo/pull/2", PRTitle: "PR 2",
	}
	if row := branches.Branches[2]; row != want {
		t.Fatalf("pull request row = %+v, want %+v", row, want)
	}
}

func TestListWorkspaceBranchesMarksDetachedHead(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "switch", "--detach")
	head := gitHead(t, repo)
	committed := unixTime(runGit(t, repo, "log", "-1", "--format=%ct"))

	branches, err := branchViewService(repo).ListWorkspaceBranches(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	want := WorkspaceBranch{Kind: WorkspaceBranchLocal, TipSHA: head, UpdatedAt: committed, CheckedOut: true, Detached: true}
	if len(branches.Branches) != 2 || branches.Branches[0] != want {
		t.Fatalf("rows = %+v, want the detached HEAD %+v first", branches.Branches, want)
	}
	if row := branches.Branches[1]; row.Name != "main" || row.CheckedOut || !row.IsDefault {
		t.Fatalf("main row = %+v, want the default branch, not checked out", row)
	}
}

func TestListWorkspaceBranchesUnsupportedForScratch(t *testing.T) {
	dir := t.TempDir()
	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Path: dir, Kind: domain.ProjectKindScratch}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: dir}}
	st.sessions["ao-2"] = domain.SessionRecord{ID: "ao-2", ProjectID: "scratch", Metadata: domain.SessionMetadata{WorkspacePath: dir}}
	for _, id := range []domain.SessionID{"ao-1", "ao-2"} {
		branches, err := (&Service{store: st}).ListWorkspaceBranches(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if branches.Supported || branches.Branches == nil || len(branches.Branches) != 0 {
			t.Fatalf("%s branches = %+v, want unsupported with no rows", id, branches)
		}
	}
}

func TestListWorkspaceBranchesForWorkspaceProjectListsCheckedOutAndPRs(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "branch", "other")
	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Path: repo, Kind: domain.ProjectKindWorkspace, Config: domain.ProjectConfig{DefaultBranch: "main"}}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "proj", Metadata: domain.SessionMetadata{Branch: "session", WorkspacePath: repo}}
	st.prs["ao-1"] = []domain.PullRequest{
		readablePR(1, "session", strings.Repeat("c", 40), time.Now()),
		readablePR(2, "child-fix", strings.Repeat("d", 40), time.Now()),
	}

	branches, err := (&Service{store: st}).ListWorkspaceBranches(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	// Local branches are left out: only a single repository can review one.
	if got, want := strings.Join(branchNames(branches.Branches), ","), "session,child-fix"; got != want || !branches.Supported {
		t.Fatalf("rows = %s (supported=%v), want %s", got, branches.Supported, want)
	}
	if row := branches.Branches[0]; !row.CheckedOut || row.PRNumber != 1 {
		t.Fatalf("checked-out row = %+v, want it annotated with PR 1", row)
	}
	if row := branches.Branches[1]; row.Kind != WorkspaceBranchPullRequest || row.PRNumber != 2 {
		t.Fatalf("second row = %+v, want PR 2", row)
	}
}
