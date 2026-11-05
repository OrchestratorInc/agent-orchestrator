package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// f03SingleRepoConfig 复用真实 Git、SQLite 与假运行时，仅保留一个显式 Repo。
func f03SingleRepoConfig(t *testing.T, f *multiRepoFixture, requestID string) ports.SpawnConfig {
	t.Helper()
	cfg := f.spawnCfgPinned(requestID, worktreeHeadOf(t, f.repoA), "")
	cfg.Repos = cfg.Repos[1:]
	return cfg
}

// TestF03SingleRepoLiveEvidence 删掉显式 Repo 判定会拒绝健康的单 Repo 并走 Legacy。
func TestF03SingleRepoLiveEvidence(t *testing.T) {
	f := newMultiRepoFixture(t)
	ctx := context.Background()
	cfg := f03SingleRepoConfig(t, f, "f03-live")
	cfg.Repos[0].BaseSHA = strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD~1"))
	rec, _, _, err := f.manager.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.VerifySessionWorktrees(ctx, rec.ID); err != nil {
		t.Fatalf("健康单 Repo 未获核验：%v", err)
	}
	row := rowsByRepo(t, f, rec.ID)["repo-a"]
	initial, err := f.manager.ObserveSessionWorktrees(ctx, rec.ID)
	if err != nil || len(initial) != 1 || initial[0].HeadSHA != cfg.Repos[0].BaseSHA ||
		initial[0].BaseSHA != cfg.Repos[0].BaseSHA || initial[0].ProjectID != f.projectAID ||
		initial[0].RepoKey != "repo-a" || initial[0].WorktreeRef != row.WorktreePath || initial[0].Dirty {
		t.Fatalf("初次现场证据错误：%+v，%v", initial, err)
	}
	marker := filepath.Join(row.WorktreePath, "agent.txt")
	if err := os.WriteFile(marker, []byte("已提交\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runManagerGit(t, row.WorktreePath, "add", "agent.txt")
	runManagerGit(t, row.WorktreePath, "commit", "-m", "agent changes")
	head := worktreeHeadOf(t, row.WorktreePath)
	if err := os.WriteFile(marker, []byte("用户待提交\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.VerifySessionWorktrees(ctx, rec.ID); err != nil {
		t.Fatalf("合法后续提交及 dirty 被拒绝：%v", err)
	}
	live, err := f.manager.ObserveSessionWorktrees(ctx, rec.ID)
	if err != nil || len(live) != 1 || live[0].HeadSHA != head || live[0].HeadSHA == row.CreationSHA ||
		live[0].BaseSHA != row.BaseSHA || live[0].Branch != row.Branch || !live[0].Dirty {
		t.Fatalf("当前 HEAD 或 dirty 仍来自历史记录：%+v，%v", live, err)
	}
	dataDir := f.manager.dataDir
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	m := New(Deps{Workspace: f.workspace, Store: reopened, DataDir: dataDir})
	if _, err := m.restoreSessionWorkspace(ctx, f.projectA, rec); err != nil {
		t.Fatalf("SQLite 重开后恢复失败：%v", err)
	}
	if restored, err := m.ObserveSessionWorktrees(ctx, rec.ID); err != nil || len(restored) != 1 || restored[0] != live[0] {
		t.Fatalf("重启前后现场证据不同：%+v，%v", restored, err)
	}
	if got := worktreeHeadOf(t, row.WorktreePath); got != head {
		t.Fatalf("恢复改变 HEAD：%s，原 %s", got, head)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "用户待提交\n" {
		t.Fatalf("核验或恢复破坏 dirty：%q，%v", data, err)
	}
}

// TestF03MissingCreationFailsClosed 删掉创建 commit 必填检查会信任缺失的历史证据。
func TestF03MissingCreationFailsClosed(t *testing.T) {
	f := newMultiRepoFixture(t)
	ctx := context.Background()
	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfg("f03-creation"))
	if err != nil {
		t.Fatal(err)
	}
	row := rowsByRepo(t, f, rec.ID)["repo-b"]
	row.CreationSHA = ""
	if err := f.store.UpsertSessionWorktree(ctx, row); err != nil {
		t.Fatal(err)
	}
	if observations, err := f.manager.ObserveSessionWorktrees(ctx, rec.ID); !errors.Is(err, ErrMultiRepoWorktreeUnverified) || observations != nil {
		t.Fatalf("无 CreationSHA 或部分证据仍可核验：%+v，%v", observations, err)
	}
}

// TestF03InvalidBaseFailsClosed 删掉 BaseSHA→CreationSHA 祖先检查会放行错误基线。
func TestF03InvalidBaseFailsClosed(t *testing.T) {
	f := newMultiRepoFixture(t)
	ctx := context.Background()
	pin := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD~1"))
	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfgPinned("f03-base", pin, ""))
	if err != nil {
		t.Fatal(err)
	}
	row := rowsByRepo(t, f, rec.ID)["repo-a"]
	row.BaseSHA = worktreeHeadOf(t, f.repoA)
	if err := f.store.UpsertSessionWorktree(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.VerifySessionWorktrees(ctx, rec.ID); !errors.Is(err, ErrMultiRepoHistoryDiverged) {
		t.Fatalf("未来 commit 被接受为创建基线：%v", err)
	}
}

// TestF03SingleRepoRestoreNeverRecreatesMissingWorktree 删掉恢复前显式 Repo 检查会重建失效工作区。
func TestF03SingleRepoRestoreNeverRecreatesMissingWorktree(t *testing.T) {
	f := newMultiRepoFixture(t)
	ctx := context.Background()
	rec, _, _, err := f.manager.Spawn(ctx, f03SingleRepoConfig(t, f, "f03-missing"))
	if err != nil {
		t.Fatal(err)
	}
	row := rowsByRepo(t, f, rec.ID)["repo-a"]
	// 只删除本用例创建的干净临时工作树，保留 SQLite 历史记录。
	runManagerGit(t, f.repoA, "worktree", "remove", row.WorktreePath)
	_, err = f.manager.restoreSessionWorkspace(ctx, f.projectA, rec)
	if !errors.Is(err, ErrMultiRepoWorktreeMissing) {
		t.Fatalf("失效单 Repo 恢复未闭锁：%v", err)
	}
	if _, err := os.Stat(row.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("恢复重建了失效工作树：%v", err)
	}
}

// TestF03SeparateGitDirectory 删掉真实 common-dir 对比会错误拒绝合法外置 Git 目录。
func TestF03SeparateGitDirectory(t *testing.T) {
	f := newMultiRepoFixture(t)
	ctx := context.Background()
	common := filepath.Join(t.TempDir(), "repository.git")
	runManagerGit(t, f.repoA, "init", "--separate-git-dir", common)
	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfg("f03-common-dir"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.VerifySessionWorktrees(ctx, rec.ID); err != nil {
		t.Fatalf("真实 common-dir 相同的合法仓库被拒绝：%v", err)
	}
}

// TestF03LegacySingleRepoCompatibility 删掉 Legacy 判定会强制旧 Session 提供 M2 身份。
func TestF03LegacySingleRepoCompatibility(t *testing.T) {
	f := newMultiRepoFixture(t)
	ctx := context.Background()
	cfg := f.spawnCfg("f03-legacy")
	cfg.Repos = nil
	rec, _, _, err := f.manager.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.VerifySessionWorktrees(ctx, rec.ID); err != nil {
		t.Fatalf("Legacy 被误判为显式 M2：%v", err)
	}
	if observations, err := f.manager.ObserveSessionWorktrees(ctx, rec.ID); err != nil || observations != nil {
		t.Fatalf("Legacy 伪造了 M2 证据：%+v，%v", observations, err)
	}
	if _, err := f.manager.restoreSessionWorkspace(ctx, f.projectA, rec); err != nil {
		t.Fatalf("Legacy 恢复行为被破坏：%v", err)
	}
	rows, err := f.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ProjectID != domain.ProjectID("") {
			t.Fatalf("Legacy 测试出现显式 Repo 身份：%+v", row)
		}
	}
}
