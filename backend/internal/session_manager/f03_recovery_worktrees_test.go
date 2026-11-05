package sessionmanager

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestF03RecoveryEntryPointsRejectMissingWorktree 删掉任一启动前核验会放行被删的单 Repo。
// 同一真实现场经过 TUI Resume、Chat 重连、live Reconcile 和关闭标记恢复，均不得新建工作区或控制器。
func TestF03RecoveryEntryPointsRejectMissingWorktree(t *testing.T) {
	f := newMultiRepoFixture(t)
	ctx := context.Background()
	rec, _, _, err := f.manager.Spawn(ctx, f03SingleRepoConfig(t, f, "f03-recovery"))
	if err != nil {
		t.Fatal(err)
	}
	row := rowsByRepo(t, f, rec.ID)["repo-a"]
	runtime := f.manager.runtime.(*fakeRuntime)
	created := runtime.created
	runtime.aliveByHandle = map[string]bool{runtimeHandle(rec.Metadata).ID: true}
	// 只移除本用例创建的干净临时工作树，保留全部持久化身份。
	runManagerGit(t, f.repoA, "worktree", "remove", row.WorktreePath)
	if _, err := f.manager.resumeAgentRecordWithPolicy(ctx, "F03 Resume", rec, false); !errors.Is(err, ErrMultiRepoWorktreeMissing) {
		t.Fatalf("TUI Resume 未先核验：%v", err)
	}
	launcher := &recordingLauncher{}
	f.manager.chat = launcher
	if _, err := f.manager.resumeChatController(ctx, "F03 reconnect", rec, f.projectA, workspaceInfo(rec),
		false, true, "", domain.SessionInterfaceTransitionHistoryStrict); !errors.Is(err, ErrMultiRepoWorktreeMissing) {
		t.Fatalf("Chat 重连未先核验：%v", err)
	}
	if err := f.manager.reconcileLive(ctx, rec); !errors.Is(err, ErrMultiRepoWorktreeMissing) {
		t.Fatalf("存活 runtime 使 Reconcile 跳过工作区：%v", err)
	}
	row.State, rec.IsTerminated = "removed", true
	if err := f.store.UpsertSessionWorktree(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	f.manager.restoreAllSession(ctx, rec)
	if _, err := os.Stat(row.WorktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("自动恢复重建了失效工作树：%v", err)
	}
	if runtime.created != created || len(launcher.started) != 0 {
		t.Fatalf("拒绝前仍启动控制器：TUI=%d，Chat=%d", runtime.created-created, len(launcher.started))
	}
	stored, found, err := f.store.GetSession(ctx, rec.ID)
	if err != nil || !found || !stored.IsTerminated {
		t.Fatalf("自动恢复改写 Session 状态：%+v，%v", stored, err)
	}
	rows, err := f.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil || len(rows) != 1 || rows[0] != row {
		t.Fatalf("自动恢复删除或改写持久化证据：%+v，%v", rows, err)
	}
}
