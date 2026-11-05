package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// round4Workspace 在真实 Git 适配器前记录创建，并可模拟适配器忽略请求 pin。
type round4Workspace struct {
	ports.Workspace
	creates   int
	ignorePin bool
}

// Create 保留真实文件系统副作用，让 Manager 的前置校验和补偿接受反向验证。
func (w *round4Workspace) Create(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	w.creates++
	if w.ignorePin {
		cfg.BaseSHA = ""
	}
	return w.Workspace.Create(ctx, cfg)
}

// TestMultiRepoRound4Preflight 验证第二仓库的非法 pin 在任何创建之前被拒绝。
func TestMultiRepoRound4Preflight(t *testing.T) {
	f := newMultiRepoFixture(t)
	w := &round4Workspace{Workspace: f.workspace}
	f.manager.workspace = w
	_, _, _, err := f.manager.Spawn(context.Background(), f.spawnCfgPinned("preflight", "", strings.Repeat("0", 40)))
	if !errors.Is(err, ports.ErrWorkspaceBaseSHAUnavailable) {
		t.Fatalf("错误 = %v", err)
	}
	if w.creates != 0 {
		t.Fatalf("非法第二仓库 pin 仍触发了 %d 次创建", w.creates)
	}
}

// TestMultiRepoRound4VerificationRollback 验证创建后的 HEAD 检查失败也能清理本次产物。
func TestMultiRepoRound4VerificationRollback(t *testing.T) {
	f := newMultiRepoFixture(t)
	w := &round4Workspace{Workspace: f.workspace, ignorePin: true}
	f.manager.workspace = w
	pin := worktreeHeadOf(t, f.repoA)
	pin = strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", pin+"~1"))
	_, _, _, err := f.manager.Spawn(context.Background(), f.spawnCfgPinned("verify-rollback", pin, ""))
	if !errors.Is(err, ErrMultiRepoWorktreeUnverified) {
		t.Fatalf("错误 = %v", err)
	}
	registered := runManagerGit(t, f.repoA, "worktree", "list", "--porcelain")
	if strings.Count(registered, "worktree ") != 1 {
		t.Fatalf("HEAD 核验失败后遗留 Worktree：\n%s", registered)
	}
}

// TestMultiRepoRound4RejectsReplacedRepository 使用相同历史、相同分支的另一仓库替换路径。
// 删掉持久化 ProjectID/RepoPath 的校验会使恢复误把替换仓库认作原始授权仓库。
func TestMultiRepoRound4RejectsReplacedRepository(t *testing.T) {
	f := newMultiRepoFixture(t)
	ctx := context.Background()
	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfgPinned("replaced-repo", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	row := rowsByRepo(t, f, rec.ID)["repo-a"]
	replacement := filepath.Join(t.TempDir(), "clone")
	runManagerGit(t, f.repoA, "clone", "--no-hardlinks", f.repoA, replacement)
	project := f.projectA
	project.ID, project.Path = "replacement", replacement
	if err := f.store.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	// 只移除本用例在临时目录里创建的干净工作树；不存在用户工作区。
	runManagerGit(t, f.repoA, "worktree", "remove", row.WorktreePath)
	runManagerGit(t, replacement, "worktree", "add", "-b", row.Branch, row.WorktreePath, row.CreationSHA)
	marker := filepath.Join(row.WorktreePath, "keep-dirty.txt")
	if err := os.WriteFile(marker, []byte("保留\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = f.manager.restoreSessionWorkspace(ctx, f.projectA, rec)
	if !errors.Is(err, ErrMultiRepoRepoMismatch) {
		t.Fatalf("替换仓库的恢复结果 = %v，应拒绝归属不一致", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("恢复破坏了 dirty 文件：%v", err)
	}
}
