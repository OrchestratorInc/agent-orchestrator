package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestM4MaterializeFrozenDeliveryTree 删除 TreeSHA 导出边界会让测试使用范围外 dirty 或忽略待交付修改。
func TestM4MaterializeFrozenDeliveryTree(t *testing.T) {
	repo := deliveryFixture(t)
	writeDeliveryFile(t, repo.WorktreePath, "src/a.txt", "delivery")
	writeDeliveryFile(t, repo.WorktreePath, "outside.txt", "uncommitted dependency")
	bound, git, err := InspectDelivery(context.Background(), repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := domain.ArtifactSnapshot{Repo: bound, Git: git, ArtifactRevision: 1}
	a.Fingerprint = a.Digest()
	dir, err := MaterializeDelivery(context.Background(), a, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"src/a.txt": "delivery", "outside.txt": "original"} {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil || string(got) != want {
			t.Fatalf("export %s=%q want=%q err=%v", path, got, want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("export exposed Git metadata: %v", err)
	}
	if err := VerifyMaterializedDelivery(context.Background(), a, dir); err != nil {
		t.Fatal(err)
	}
	writeDeliveryFile(t, dir, "src/a.txt", "changed by test")
	if err := VerifyMaterializedDelivery(context.Background(), a, dir); err == nil {
		t.Fatal("modified tested source accepted")
	}
	got, err := os.ReadFile(filepath.Join(repo.WorktreePath, "outside.txt"))
	if err != nil || string(got) != "uncommitted dependency" {
		t.Fatal("export changed worker files")
	}
	a.Git.TreeSHA = repo.BaseSHA
	if _, err := MaterializeDelivery(context.Background(), a, t.TempDir()); err == nil {
		t.Fatal("modified fingerprint accepted")
	}
}
