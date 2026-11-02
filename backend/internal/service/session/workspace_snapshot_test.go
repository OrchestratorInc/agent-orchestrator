package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestSnapshotBrowsesUnchangedFilesWithoutTouchingWorkspace(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	commitWorkspaceFile(t, repo, "src/unchanged.txt", "original\n", "base file")
	runGit(t, repo, "switch", "-c", "feature")
	tip := commitWorkspaceFile(t, repo, "src/changed.txt", "feature\n", "feature file")
	runGit(t, repo, "switch", "main")
	writeWorkspaceFile(t, repo, "src/unchanged.txt", "staged\n")
	runGit(t, repo, "add", "src/unchanged.txt")
	writeWorkspaceFile(t, repo, "src/unchanged.txt", "dirty\n")
	head, status := gitHead(t, repo), runGit(t, repo, "status", "--porcelain")
	indexPath := filepath.Join(repo, ".git", "index")
	index, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	svc := branchViewService(repo)
	input := WorkspaceSelectionRevisionInput{View: WorkspaceView{Branch: "feature", ExpectedTipSHA: tip}, Path: "src"}
	tree, err := svc.ListWorkspaceSnapshotTree(context.Background(), "ao-1", input, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Entries) != 3 || tree.Entries[2].Name != "unchanged.txt" {
		t.Fatalf("tree=%+v", tree)
	}
	input.Path = "src/unchanged.txt"
	file, err := svc.GetWorkspaceSnapshotFile(context.Background(), "ao-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if file.Content != "original\n" || file.Status != WorkspaceFileUnmodified || file.Editable || !file.Historical {
		t.Fatalf("file=%+v", file)
	}
	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(index) || gitHead(t, repo) != head || runGit(t, repo, "status", "--porcelain") != status {
		t.Fatal("snapshot read changed the workspace")
	}
	input.Path = ""
	filtered, err := svc.ListWorkspaceSnapshotTree(context.Background(), "ao-1", input, "unchanged")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Entries) != 1 || filtered.Entries[0].Path != "src/unchanged.txt" {
		t.Fatalf("filtered=%+v", filtered)
	}
}

func TestSnapshotReadsCombinedSelectionAndDeletedOldSide(t *testing.T) {
	repo, base, commits := selectionFixture(t)
	svc := checkedOutSelectionService(repo, base)
	input := WorkspaceSelectionRevisionInput{CommitSHAs: []string{commits[0], commits[2]}, Path: ""}
	tree, err := svc.ListWorkspaceSnapshotTree(context.Background(), "ao-1", input, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range tree.Entries {
		if entry.Path == "b.txt" {
			t.Fatal("excluded commit leaked into snapshot")
		}
	}
	input.Path = "c.txt"
	detail, err := svc.GetWorkspaceSnapshotFile(context.Background(), "ao-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Content != "c\n" {
		t.Fatalf("content=%q", detail.Content)
	}
	runGit(t, repo, "rm", "a.txt")
	runGit(t, repo, "commit", "-m", "delete a")
	input = WorkspaceSelectionRevisionInput{CommitSHA: gitHead(t, repo), Path: "a.txt"}
	detail, err = svc.GetWorkspaceSnapshotFile(context.Background(), "ao-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Deleted || detail.Content != "a\n" || detail.Diff == "" {
		t.Fatalf("deleted=%+v", detail)
	}
}

func TestSnapshotRejectsMovedSourcesAndEscapingPaths(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "switch", "-c", "feature")
	tip := commitWorkspaceFile(t, repo, "a.txt", "a\n", "a")
	commitWorkspaceFile(t, repo, "a.txt", "new\n", "advance")
	svc := branchViewService(repo)
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "main"))
	input := WorkspaceSelectionRevisionInput{Snapshot: true, Path: "a.txt", Scope: WorkspaceDiffCommitted, Side: WorkspaceBlobAfter, View: WorkspaceView{Branch: "feature", ExpectedTipSHA: tip, ExpectedBaseSHA: base}}
	file, err := svc.GetWorkspaceSnapshotFile(context.Background(), "ao-1", input)
	if err != nil || file.Content != "a\n" {
		t.Fatalf("pinned file=%+v error=%v", file, err)
	}
	revision, err := svc.GetWorkspaceSelectionFileRevision(context.Background(), "ao-1", input)
	if err != nil || revision.Content != "a\n" {
		t.Fatalf("pinned context=%+v error=%v", revision, err)
	}
	runGit(t, repo, "update-ref", "refs/heads/feature", base)
	_, err = svc.GetWorkspaceSnapshotFile(context.Background(), "ao-1", input)
	requireAPIError(t, err, apierr.KindConflict, "WORKSPACE_REF_MOVED")
	_, err = svc.GetWorkspaceSnapshotFile(context.Background(), "ao-1", WorkspaceSelectionRevisionInput{Path: "../outside", View: WorkspaceView{Branch: "feature"}})
	if err == nil {
		t.Fatal("accepted an escaping path")
	}
}

func TestSnapshotEmptyComparisonStillListsCompleteTree(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	tip := gitHead(t, repo)
	svc := checkedOutSelectionService(repo, tip)
	tree, err := svc.ListWorkspaceSnapshotTree(context.Background(), "ao-1", WorkspaceSelectionRevisionInput{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Entries) == 0 {
		t.Fatal("empty diff hid the complete repository")
	}
}

func TestSnapshotReadsKnownRemoteBranchWithoutCheckout(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "switch", "-c", "feature")
	tip := commitWorkspaceFile(t, repo, "remote.txt", "remote version\n", "remote content")
	runGit(t, repo, "update-ref", "refs/remotes/origin/feature", tip)
	runGit(t, repo, "switch", "main")
	runGit(t, repo, "branch", "origin/feature")
	head := gitHead(t, repo)
	svc := branchViewService(repo)
	branches, err := svc.ListWorkspaceBranches(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	local, remote := false, false
	for _, branch := range branches.Branches {
		if branch.Name == "origin/feature" {
			local = local || branch.Kind == WorkspaceBranchLocal
			remote = remote || branch.Kind == WorkspaceBranchRemote
		}
	}
	if !local || !remote {
		t.Fatalf("local and remote branches collided: %+v", branches.Branches)
	}
	file, err := svc.GetWorkspaceSnapshotFile(context.Background(), "ao-1", WorkspaceSelectionRevisionInput{Path: "remote.txt", View: WorkspaceView{Branch: "refs/remotes/origin/feature", ExpectedTipSHA: tip}})
	if err != nil {
		t.Fatal(err)
	}
	if file.Content != "remote version\n" || gitHead(t, repo) != head {
		t.Fatalf("remote snapshot=%+v", file)
	}
}

func TestSnapshotPRUnchangedFileUsesItsAssociatedRepository(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	base := commitWorkspaceFile(t, repo, "unchanged.txt", "from PR repository\n", "base")
	runGit(t, repo, "switch", "-c", "feature")
	tip := commitWorkspaceFile(t, repo, "changed.txt", "changed\n", "feature")
	runGit(t, repo, "switch", "main")
	writeWorkspaceFile(t, repo, "unchanged.txt", "live workspace\n")
	runGit(t, repo, "remote", "add", "origin", "https://github.com/acme/repo.git")
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	st.prs["ao-1"] = []domain.PullRequest{{Number: 42, URL: "https://github.com/acme/repo/pull/42", Provider: "github", Host: "github.com", Repo: "acme/repo", SourceBranch: "feature", TargetBranch: "main", BaseSHA: base, HeadSHA: tip}}
	svc := &Service{store: st}
	file, err := svc.GetWorkspaceSnapshotFile(context.Background(), "ao-1", WorkspaceSelectionRevisionInput{Path: "unchanged.txt", View: WorkspaceView{PRNumber: 42, SourceURL: "https://github.com/acme/repo/pull/42", ExpectedTipSHA: tip}})
	if err != nil {
		t.Fatal(err)
	}
	if file.Content != "from PR repository\n" || file.Editable {
		t.Fatalf("PR snapshot=%+v", file)
	}
}
