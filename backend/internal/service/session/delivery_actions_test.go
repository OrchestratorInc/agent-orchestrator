package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func deliveryActionService(t *testing.T) (*Service, string) {
	t.Helper()
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "ao/card")
	remote := filepath.Join(t.TempDir(), "acme", "widget.git")
	if err := os.MkdirAll(filepath.Dir(remote), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, filepath.Dir(remote), "init", "--bare", remote)
	runGit(t, repo, "remote", "add", "origin", remote)
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "project-1", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: repo, DiffBaseSHA: runGit(t, repo, "rev-parse", "HEAD"), DiffBaseRef: "main"}}
	st.projects["project-1"] = domain.ProjectRecord{ID: "project-1", Path: repo, RepoOriginURL: remote}
	return NewWithDeps(Deps{Store: st}), repo
}

func TestAdvanceDeliveryRejectsStaleWorkspaceBeforeMutation(t *testing.T) {
	svc, repo := deliveryActionService(t)
	writeWorkspaceFile(t, repo, "new.txt", "new\n")
	head := runGit(t, repo, "rev-parse", "HEAD")
	_, err := svc.AdvanceDelivery(context.Background(), "ao-1", DeliveryActionInput{Action: DeliveryActionCommitAndPublish, ExpectedWorkspaceVersion: "stale", CommitMessage: "feat: publish"})
	if err == nil {
		t.Fatal("stale delivery succeeded")
	}
	if got := runGit(t, repo, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD changed: %q != %q", got, head)
	}
	if got := runGit(t, repo, "status", "--porcelain"); got == "" {
		t.Fatal("dirty work was lost")
	}
}

func TestAdvanceDeliveryRequiresCommitMessageBeforeStaging(t *testing.T) {
	svc, repo := deliveryActionService(t)
	writeWorkspaceFile(t, repo, "new.txt", "new\n")
	files, err := svc.ListWorkspaceFiles(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.AdvanceDelivery(context.Background(), "ao-1", DeliveryActionInput{Action: DeliveryActionCommitAndPublish, ExpectedWorkspaceVersion: files.WorkspaceVersion})
	if err == nil {
		t.Fatal("empty commit message succeeded")
	}
	if got := runGit(t, repo, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("files were staged before validation: %q", got)
	}
}

func TestAdvanceDeliveryCommitSurvivesPushFailureAsRecoverableCommit(t *testing.T) {
	svc, repo := deliveryActionService(t)
	st := svc.store.(*fakeStore)
	st.prs["ao-1"] = []domain.PullRequest{{URL: "https://example.com/acme/widget/pull/42", Number: 42, Repo: "acme/widget", SourceBranch: "ao/card"}}
	// Match the local-path origin normalization used by the delivery snapshot.
	facts := readDeliveryGitFacts(context.Background(), repo)
	st.prs["ao-1"][0].Repo = facts.remoteRepo
	writeWorkspaceFile(t, repo, "new.txt", "new\n")
	files, err := svc.ListWorkspaceFiles(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.Delivery.Action != DeliveryActionCommitAndPush {
		t.Fatalf("action = %q", files.Delivery.Action)
	}
	// Leave the reviewed destination unchanged but make transport fail.
	remote := strings.TrimSpace(runGit(t, repo, "remote", "get-url", "origin"))
	if err := os.RemoveAll(remote); err != nil {
		t.Fatal(err)
	}
	_, err = svc.AdvanceDelivery(context.Background(), "ao-1", DeliveryActionInput{Action: DeliveryActionCommitAndPush, ExpectedWorkspaceVersion: files.WorkspaceVersion, CommitMessage: "feat: keep commit"})
	if err == nil {
		t.Fatal("push unexpectedly succeeded")
	}
	if got := runGit(t, repo, "log", "-1", "--pretty=%s"); got != "feat: keep commit\n" {
		t.Fatalf("commit subject = %q", got)
	}
	if got := runGit(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("worktree not preserved as committed state: %q", got)
	}
}

func TestDeliveryRemoteBranchMatchesHeadRecognizesCompletedPush(t *testing.T) {
	_, repo := deliveryActionService(t)
	if matches, err := deliveryRemoteBranchMatchesHead(context.Background(), repo, "ao/card"); err != nil || matches {
		t.Fatalf("before push: matches=%v err=%v", matches, err)
	}
	runGit(t, repo, "push", "origin", "HEAD:refs/heads/ao/card")
	if matches, err := deliveryRemoteBranchMatchesHead(context.Background(), repo, "ao/card"); err != nil || !matches {
		t.Fatalf("after push: matches=%v err=%v", matches, err)
	}
}
