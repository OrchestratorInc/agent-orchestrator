package gitworktree

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestM4IntegrationPreviewIsolatedAndReadOnly 删除 detached Worktree、merge 证据或清理会把预演写入目标 ref。
func TestM4IntegrationPreviewIsolatedAndReadOnly(t *testing.T) {
	repo := deliveryFixture(t)
	writeDeliveryFile(t, repo.WorktreePath, "src/a.txt", "task change")
	bound, git, err := InspectDelivery(context.Background(), repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := domain.ArtifactSnapshot{Repo: bound, Git: git, ArtifactRevision: 1, CreatedAt: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)}
	a.Fingerprint = a.Digest()
	sha, err := PrepareDeliveryCommit(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	guard := func(ctx context.Context, write func(context.Context) error) error { return write(ctx) }
	targetBefore := strings.TrimSpace(string(deliveryTestGit(t, repo.RepoPath, "rev-parse", repo.TargetRef)))
	if err := ApplyDeliveryCommit(context.Background(), a, sha, targetBefore, t.TempDir(), guard); err != nil {
		t.Fatal(err)
	}
	worktrees := string(deliveryTestGit(t, repo.RepoPath, "worktree", "list", "--porcelain"))
	result, err := PreviewDelivery(context.Background(), a, sha, filepath.Join(t.TempDir(), "preview"))
	if err != nil || result.Status != "clean" || result.DiffDigest == "" || !domain.ValidGitSHA(result.MergeCommitSHA) {
		t.Fatalf("clean preview=%+v err=%v", result, err)
	}
	if got := strings.TrimSpace(string(deliveryTestGit(t, repo.RepoPath, "rev-parse", repo.TargetRef))); got != targetBefore {
		t.Fatalf("target ref changed: %s", got)
	}

	writeDeliveryFile(t, repo.RepoPath, "src/a.txt", "target change")
	deliveryTestGit(t, repo.RepoPath, "add", "--", "src/a.txt")
	deliveryTestGit(t, repo.RepoPath, "-c", "user.name=M4 Test", "-c", "user.email=m4@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "target change")
	conflict, err := PreviewDelivery(context.Background(), a, sha, filepath.Join(t.TempDir(), "preview"))
	if err != nil || conflict.Status != "conflict" || len(conflict.ConflictPaths) != 1 || conflict.ConflictPaths[0] != "src/a.txt" {
		t.Fatalf("conflict preview=%+v err=%v", conflict, err)
	}
	if got := string(deliveryTestGit(t, repo.RepoPath, "worktree", "list", "--porcelain")); strings.Count(got, "worktree ") != strings.Count(worktrees, "worktree ") {
		t.Fatal("preview left registered worktrees")
	}
	writeDeliveryFile(t, repo.WorktreePath, "outside.txt", "changed after commit")
	if _, err := PreviewDelivery(context.Background(), a, sha, t.TempDir()); !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("dirty changed after commit: %v", err)
	}
}
