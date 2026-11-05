package sessionmanager

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// f03BindingIntent 通过真实 SQLite 事务创建 Workflow、Claim 与未绑定 Session 的 Repo 意图。
func f03BindingIntent(t *testing.T, f *multiRepoFixture, cfg ports.SpawnConfig) domain.WorkflowAttemptBinding {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	wf := domain.Workflow{ID: "f03-workflow", Title: "F03", Goal: "核验持久化归属",
		State: domain.WorkflowStateDraft, Revision: 1, CreatedAt: now, UpdatedAt: now}
	task := domain.WorkflowTask{ID: "f03-task", WorkflowID: wf.ID, Title: "核验",
		Kind: domain.WorkflowTaskKindDevelopment, State: domain.WorkflowTaskStatePending,
		MaxAttempts: 2, Revision: 1, CreatedAt: now, UpdatedAt: now}
	var repositories []domain.WorkflowRepository
	var expected []domain.WorkflowAttemptRepoBinding
	for _, repo := range cfg.Repos {
		repositories = append(repositories, domain.WorkflowRepository{WorkflowID: wf.ID,
			RepoKey: domain.WorkflowRepoKey(repo.RepoKey), ProjectID: repo.ProjectID, BaseSHA: repo.BaseSHA})
		expected = append(expected, domain.WorkflowAttemptRepoBinding{
			RepoKey: domain.WorkflowRepoKey(repo.RepoKey), ProjectID: repo.ProjectID,
			ExpectedBaseSHA: repo.BaseSHA, State: domain.WorkflowRepoBindingStatePending})
	}
	fingerprint := strings.Repeat("a", 64)
	if _, _, err := f.store.CreateWorkflow(ctx, wf, repositories, []domain.WorkflowTask{task}, nil, nil, "f03-create", fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.ActivateWorkflow(ctx, wf.ID, 1, "f03-activate", fingerprint, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	ready, found, err := f.store.GetWorkflowTask(ctx, wf.ID, task.ID)
	if err != nil || !found {
		t.Fatalf("读取任务：%v，%v", found, err)
	}
	leaseUntil := now.Add(time.Hour)
	attempt, _, err := f.store.ClaimWorkflowTask(ctx, domain.WorkflowAttempt{
		ID: "f03-attempt", WorkflowID: wf.ID, TaskID: task.ID, WorkerID: "f03-worker",
		LeaseToken: "仅本地测试", LeaseUntil: &leaseUntil, State: domain.WorkflowAttemptStateClaimed,
		CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second),
	}, ready.Revision, "f03-claim", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := f.store.CreateWorkflowAttemptBinding(ctx, domain.WorkflowAttemptBinding{
		AttemptID: attempt.ID, WorkflowID: wf.ID, TaskID: task.ID,
		SpawnRequestID: cfg.ClientRequestID, SpawnHash: fingerprint,
	}, expected, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

// TestF03DurableRepoSetFencesSingleWorktree 删掉按 key/Project/pin/ref 对齐会把替换行当作授权集合。
// 首次 Bind 前通过 Spawn 意图定位，Bind 后通过 Session 定位；删行仍保留这两条证据链。
func TestF03DurableRepoSetFencesSingleWorktree(t *testing.T) {
	f := newMultiRepoFixture(t)
	ctx := context.Background()
	cfg := f03SingleRepoConfig(t, f, "ordinary-request-id")
	binding := f03BindingIntent(t, f, cfg)
	rec, _, _, err := f.manager.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if observations, err := f.manager.ObserveSessionWorktrees(ctx, rec.ID); err != nil || len(observations) != 1 {
		t.Fatalf("首次 Bind 前无法核验：%+v，%v", observations, err)
	}
	row := rowsByRepo(t, f, rec.ID)["repo-a"]
	if err := f.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	wrongKey := row
	wrongKey.RepoName = "unassigned-repo"
	if err := f.store.UpsertSessionWorktree(ctx, wrongKey); err != nil {
		t.Fatal(err)
	}
	if observations, err := f.manager.ObserveSessionWorktrees(ctx, rec.ID); !errors.Is(err, ErrMultiRepoWorktreeMissing) || observations != nil {
		t.Fatalf("同数量但不同 key 的集合被放行：%+v，%v", observations, err)
	}
	if err := f.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.restoreSessionWorkspace(ctx, f.projectA, rec); !errors.Is(err, ErrMultiRepoWorktreeMissing) {
		t.Fatalf("未写 SessionID 的意图无法闭锁记录丢失：%v", err)
	}
	if err := f.store.UpsertSessionWorktree(ctx, row); err != nil {
		t.Fatal(err)
	}
	bound, _, err := f.store.BindWorkflowAttemptSession(ctx, binding.AttemptID, rec.ID, "f03-generation",
		domain.WorkflowBindingStateBoundReady, binding.Revision, time.Now().UTC().Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if observations, err := f.manager.ObserveSessionWorktrees(ctx, rec.ID); err != nil || len(observations) != 1 {
		t.Fatalf("Bind 后无法核验：%+v，%v", observations, err)
	}
	// 共用的绑定身份分支覆盖 ProjectID 与 pin，不穷举空值。
	wrongProject, wrongPin := row, row
	wrongProject.ProjectID = f.projectBID
	wrongPin.BaseSHA = strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD~1"))
	for _, changed := range []domain.SessionWorktreeRecord{wrongProject, wrongPin} {
		if err := f.store.UpsertSessionWorktree(ctx, changed); err != nil {
			t.Fatal(err)
		}
		if observations, err := f.manager.ObserveSessionWorktrees(ctx, rec.ID); !errors.Is(err, ErrMultiRepoRepoMismatch) || observations != nil {
			t.Fatalf("偏离绑定的行被放行：%+v，%v", observations, err)
		}
	}
	if err := f.store.UpsertSessionWorktree(ctx, row); err != nil {
		t.Fatal(err)
	}
	expected, err := f.store.ListWorkflowAttemptRepoBindings(ctx, bound.AttemptID)
	if err != nil || len(expected) != 1 {
		t.Fatalf("读取 Repo 意图：%+v，%v", expected, err)
	}
	proof := expected[0]
	proof.SessionWorktreeRef, proof.ObservedHeadSHA, proof.State = row.WorktreePath, row.CreationSHA, domain.WorkflowRepoBindingStateVerified
	if _, _, err := f.store.RecordWorkflowAttemptRepoBinding(ctx, proof, proof.Revision, time.Now().UTC().Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	// 真正移动同库、同历史、同分支工作树，并更新 Session；独立 binding ref 仍必须拒绝替换。
	moved := filepath.Join(f.managedRoot, "moved-worktree")
	runManagerGit(t, f.repoA, "worktree", "move", row.WorktreePath, moved)
	row.WorktreePath, rec.Metadata.WorkspacePath = moved, moved
	if err := f.store.UpsertSessionWorktree(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if observations, err := f.manager.ObserveSessionWorktrees(ctx, rec.ID); !errors.Is(err, ErrMultiRepoRepoMismatch) || observations != nil {
		t.Fatalf("独立 worktree ref 未阻止合法 Git 路径替换：%+v，%v", observations, err)
	}
	if err := f.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if observations, err := f.manager.ObserveSessionWorktrees(ctx, rec.ID); !errors.Is(err, ErrMultiRepoWorktreeMissing) || observations != nil {
		t.Fatalf("已 Bind 的记录丢失被降级 Legacy：%+v，%v", observations, err)
	}
}

// f03UnreadableRepoBindings 保留真实 Session/SQLite 读取，仅移除绑定 Repo 的可读能力。
type f03UnreadableRepoBindings struct {
	Store
	binding domain.WorkflowAttemptBinding
}

// GetWorkflowAttemptBindingBySession 明确证明 Session 已属于 Workflow。
func (s f03UnreadableRepoBindings) GetWorkflowAttemptBindingBySession(context.Context, domain.SessionID) (domain.WorkflowAttemptBinding, bool, error) {
	return s.binding, true, nil
}

// TestF03BoundSessionRequiresRepoReader 删掉预期集合读取能力检查会把未知绑定降级 Legacy。
func TestF03BoundSessionRequiresRepoReader(t *testing.T) {
	f := newMultiRepoFixture(t)
	ctx := context.Background()
	cfg := f03SingleRepoConfig(t, f, "f03-unreadable")
	binding := f03BindingIntent(t, f, cfg)
	rec, _, _, err := f.manager.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err = f.store.BindWorkflowAttemptSession(ctx, binding.AttemptID, rec.ID, "f03-generation",
		domain.WorkflowBindingStateBoundReady, binding.Revision, time.Now().UTC().Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	f.manager.store = f03UnreadableRepoBindings{Store: f.store, binding: binding}
	if observations, err := f.manager.ObserveSessionWorktrees(ctx, rec.ID); !errors.Is(err, ErrMultiRepoWorktreeUnverified) || observations != nil {
		t.Fatalf("缺少 Repo reader 的绑定被放行：%+v，%v", observations, err)
	}
}
