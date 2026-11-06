package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// TestM4SnapshotPreservesIndexAndBindsOutsideChanges 删除私有 index 或完整 workspace 摘要会使保全/失效断言变红。
func TestM4SnapshotPreservesIndexAndBindsOutsideChanges(t *testing.T) {
	repo := deliveryFixture(t)
	writeDeliveryFile(t, repo.WorktreePath, "outside.txt", "staged-user-data")
	deliveryTestGit(t, repo.WorktreePath, "add", "--", "outside.txt")
	writeDeliveryFile(t, repo.WorktreePath, "src/a.txt", "new-content")
	writeDeliveryFile(t, repo.WorktreePath, "src/b.bin", string([]byte{0, 1, 255, 0}))
	writeDeliveryFile(t, repo.WorktreePath, "private.txt", "unrelated-secret-content")
	privateBlob, err := deliveryGit(context.Background(), repo.WorktreePath, "", []byte("unrelated-secret-content"), "hash-object", "--stdin", "--no-filters")
	if err != nil {
		t.Fatal(err)
	}
	index := deliveryTestGit(t, repo.WorktreePath, "ls-files", "--stage", "-z")
	bound, artifact, err := InspectDelivery(context.Background(), repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.Files) != 2 || bound.GitCommonDir == "" || artifact.HeadSHA != repo.BaseSHA {
		t.Fatalf("unexpected snapshot: %+v", artifact)
	}
	if _, err := deliveryGit(context.Background(), repo.WorktreePath, "", nil, "cat-file", "-e", strings.TrimSpace(string(privateBlob))); err == nil {
		t.Fatal("outside secret was written to object database")
	}
	if got := deliveryTestGit(t, repo.WorktreePath, "ls-files", "--stage", "-z"); !bytes.Equal(got, index) {
		t.Fatal("user index changed")
	}
	if got := string(deliveryTestGit(t, repo.WorktreePath, "show", artifact.TreeSHA+":outside.txt")); got != "original" {
		t.Fatalf("outside dirty entered tree: %q", got)
	}
	if got := string(deliveryTestGit(t, repo.WorktreePath, "show", artifact.TreeSHA+":src/a.txt")); got != "new-content" {
		t.Fatalf("delivery content: %q", got)
	}
	writeDeliveryFile(t, repo.WorktreePath, "outside.txt", "changed-after-gate")
	_, next, err := InspectDelivery(context.Background(), bound, t.TempDir())
	if err != nil || next.TreeSHA != artifact.TreeSHA || next.WorkspaceDigest == artifact.WorkspaceDigest {
		t.Fatalf("outside change did not invalidate: %+v %v", next, err)
	}
	if got := deliveryTestGit(t, repo.WorktreePath, "ls-files", "--stage", "-z"); !bytes.Equal(got, index) {
		t.Fatal("user index changed on repeat")
	}
}

// TestM4SnapshotRejectsRepoAndBranchDrift 删除 common-dir/branch 验证会接受同历史换仓或错误分支。
func TestM4SnapshotRejectsRepoAndBranchDrift(t *testing.T) {
	repo := deliveryFixture(t)
	other := t.TempDir()
	deliveryTestGit(t, other, "-c", "protocol.file.allow=always", "clone", "--local", "--no-hardlinks", repo.RepoPath, "copy")
	wrong := repo
	wrong.RepoPath = filepath.Join(other, "copy")
	if _, _, err := InspectDelivery(context.Background(), wrong, t.TempDir()); !errors.Is(err, domain.ErrArtifactInvalid) {
		t.Fatalf("foreign repository accepted: %v", err)
	}
	deliveryTestGit(t, repo.WorktreePath, "checkout", "-b", "another-task")
	if _, _, err := InspectDelivery(context.Background(), repo, t.TempDir()); !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("branch drift accepted: %v", err)
	}
}

// TestM4SnapshotRejectsUnsafeInputs 删除 scope 或磁盘 symlink/config 检查会信任越界或外部可执行配置。
func TestM4SnapshotRejectsUnsafeInputs(t *testing.T) {
	repo := deliveryFixture(t)
	invalid := repo
	invalid.AllowedPaths = []string{"../secrets"}
	if _, _, err := InspectDelivery(context.Background(), invalid, t.TempDir()); !errors.Is(err, domain.ErrArtifactInvalid) {
		t.Fatalf("traversal: %v", err)
	}
	writeDeliveryFile(t, repo.WorktreePath, "src/a.txt", "modified")
	repo.AllowedPaths = []string{"src/a"}
	_, artifact, err := InspectDelivery(context.Background(), repo, t.TempDir())
	if err != nil || len(artifact.Files) != 0 {
		t.Fatalf("component boundary: %+v %v", artifact, err)
	}
	deliveryTestGit(t, repo.WorktreePath, "config", "filter.evil.clean", "this-command-must-not-run")
	if _, _, err := InspectDelivery(context.Background(), repo, t.TempDir()); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("unsafe config: %v", err)
	}
	deliveryTestGit(t, repo.WorktreePath, "config", "--unset", "filter.evil.clean")
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo.WorktreePath, "src", "linked")
	if runtime.GOOS == "windows" {
		// Windows 普通账户不能创建文件 symlink；junction 同样能让目录内读取逃出授权根。
		cmd := process.Command("cmd", "/c", "mklink", "/J", link, filepath.Dir(outside))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v %s", err, out)
		}
		t.Cleanup(func() { _ = os.Remove(link) })
	} else if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InspectDelivery(context.Background(), repo, t.TempDir()); !errors.Is(err, domain.ErrArtifactInvalid) {
		t.Fatalf("symlink accepted: %v", err)
	}
}

// TestM4SnapshotRejectsHiddenChanges 删除提交范围检查或 skip-worktree 检查，会放行清单中隐藏的越界修改或误删。
func TestM4SnapshotRejectsHiddenChanges(t *testing.T) {
	t.Run("committed outside scope", func(t *testing.T) {
		repo := deliveryFixture(t)
		writeDeliveryFile(t, repo.WorktreePath, "outside.txt", "outside commit")
		deliveryTestGit(t, repo.WorktreePath, "add", "--", "outside.txt")
		deliveryTestGit(t, repo.WorktreePath, "-c", "user.name=M4 Test", "-c", "user.email=m4@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "outside scope")
		if _, _, err := InspectDelivery(context.Background(), repo, t.TempDir()); !errors.Is(err, domain.ErrDeliveryBlocked) {
			t.Fatalf("committed outside changes accepted: %v", err)
		}
	})
	t.Run("omitted skip-worktree path", func(t *testing.T) {
		repo := deliveryFixture(t)
		deliveryTestGit(t, repo.WorktreePath, "update-index", "--skip-worktree", "--", "src/a.txt")
		if err := os.Remove(filepath.Join(repo.WorktreePath, "src", "a.txt")); err != nil {
			t.Fatal(err)
		}
		before := deliveryTestGit(t, repo.WorktreePath, "ls-files", "--stage", "-z")
		if _, _, err := InspectDelivery(context.Background(), repo, t.TempDir()); !errors.Is(err, domain.ErrDeliveryBlocked) {
			t.Fatalf("omitted file became a deletion: %v", err)
		}
		if got := deliveryTestGit(t, repo.WorktreePath, "ls-files", "--stage", "-z"); !bytes.Equal(before, got) {
			t.Fatal("refusal changed the user's index")
		}
	})
}

// deliveryFixture 建立真实临时仓库与独立任务 Worktree，不读取用户 AO 状态或启动 Harness。
func deliveryFixture(t *testing.T) domain.DeliveryRepo {
	t.Helper()
	root := t.TempDir()
	deliveryTestGit(t, root, "init", "--initial-branch=main")
	writeDeliveryFile(t, root, "src/a.txt", "original")
	writeDeliveryFile(t, root, "outside.txt", "original")
	deliveryTestGit(t, root, "add", "--", "src/a.txt", "outside.txt")
	deliveryTestGit(t, root, "-c", "user.name=M4 Test", "-c", "user.email=m4@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "base")
	head := strings.TrimSpace(string(deliveryTestGit(t, root, "rev-parse", "HEAD")))
	worktree := filepath.Join(t.TempDir(), "worktree")
	deliveryTestGit(t, root, "worktree", "add", "-b", "task/m4", worktree, head)
	return domain.DeliveryRepo{RepoKey: "repo", ProjectID: "project", RepoPath: root, WorktreePath: worktree, Branch: "task/m4", TargetRef: "refs/heads/main", BaseSHA: head, CreationSHA: head, AllowedPaths: []string{"src"}}
}

func deliveryTestGit(t *testing.T, root string, args ...string) []byte {
	t.Helper()
	out, err := deliveryGit(context.Background(), root, "", nil, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func writeDeliveryFile(t *testing.T, root, path, data string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
