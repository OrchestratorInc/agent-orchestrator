package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// TestM5PlanRejectsChangedExecutionFacts 保留已完成 Planner，令另一个任务通过真实调度变成 running/unknown。
// 删除事务内状态摘要或全图 UNKNOWN 门禁会错误应用候选；本例容量 2 只用于制造并存状态，容量 1 收尾另测。
func TestM5PlanRejectsChangedExecutionFacts(t *testing.T) {
	f, d := newM5Fixture(t)
	ctx := context.Background()
	m3Policy(t, f, "", 2)
	sha := strings.TrimSpace(m2smGit(t, f.repoA, "rev-parse", "HEAD"))
	// 用内部创建入口限定 Planner scope，保留其运行身份而不与 worker 的路径锁冲突。
	// 自然目标入口及全仓 Planner 收尾由容量 1 用例覆盖，本例不释放任何占位。
	created, _, err := f.m3.workflows.Create(ctx, "boundary-plan", workflow.CreateInput{
		Title: "候选执行事实门禁", Goal: "验证并存执行状态",
		Repositories:     []workflow.RepositoryInput{{RepoKey: "repo-a", ProjectID: f.projectA, BaseSHA: sha, TargetRef: "refs/heads/main"}},
		Tasks:            []workflow.TaskInput{{ID: "boundary-planner", Kind: domain.WorkflowTaskKindAnalysis, Title: "边界规划", Planner: true, AgentHarness: domain.HarnessCodex, Model: "planner-model"}},
		TaskRepositories: []workflow.TaskRepositoryInput{{TaskID: "boundary-planner", RepoKey: "repo-a", BaseSHA: sha, AllowedPaths: []string{"planning"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := created.ID
	path := "/workflows/" + string(w)
	m4HTTP[controllers.WorkflowEnvelope](t, f, http.MethodPost, path+"/activate", map[string]any{"expectedRevision": created.Revision, "idempotencyKey": "boundary-activate"})
	planner := m5Dispatch(t, f)
	patch := m5Patch(f, "boundary-worker")
	// 工作任务限定独立子目录；路径仲裁仍由正式调度器处理。
	patch.TaskRepositories[0].AllowedPaths = []string{"src"}
	m5Reply(t, f, d, planner, patch)
	def, err := f.m3.workflows.GetDefinition(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	input := workflow.PlanInput{SourceAttemptID: planner.ID, ExpectedRevision: def.Workflow.Revision, IdempotencyKey: "boundary-initial"}
	first := m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, path+"/plan-candidates", input).Candidate
	first = m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, path+"/plan-candidates/"+first.ID+"/confirm", controllers.ConfirmWorkflowPlanRequest{Digest: first.Digest, ExpectedRevision: first.ExpectedRevision}).Candidate
	extra := m5Patch(f, "extra-task")
	input.ExpectedRevision, input.Edited, input.IdempotencyKey = first.ConfirmedRevision, &extra, "boundary-before-running"
	prepared := m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, path+"/plan-candidates", input).Candidate
	m5Resume(t, f, w)
	worker := m5Dispatch(t, f)
	if worker.TaskID != "boundary-worker" {
		t.Fatalf("未启动目标任务: %+v", worker)
	}
	confirm := controllers.ConfirmWorkflowPlanRequest{Digest: prepared.Digest, ExpectedRevision: prepared.ExpectedRevision}
	if r := f.asOperator(http.MethodPost, path+"/plan-candidates/"+prepared.ID+"/confirm", m3JSON(t, confirm)); r.Status != http.StatusConflict {
		t.Fatalf("准备后执行状态漂移未拒绝: %d %s", r.Status, r.Raw)
	}
	extra.RetireTaskIDs = []domain.WorkflowTaskID{worker.TaskID}
	input.Edited, input.IdempotencyKey = &extra, "boundary-retire-running"
	if r := f.asOperator(http.MethodPost, path+"/plan-candidates", m3JSON(t, input)); r.Status != http.StatusBadRequest {
		t.Fatalf("仍运行任务被停用: %d %s", r.Status, r.Raw)
	}
	// 模拟心跳中断，使用正式过期恢复生成 UNKNOWN，不直接改库或释放占位。
	f.m3.clock.advance(3 * time.Minute)
	if _, err := f.store.RecoverExpiredWorkflowAttempts(ctx, f.m3.clock.Now()); err != nil {
		t.Fatal(err)
	}
	unknown, found, err := f.store.GetWorkflowAttempt(ctx, worker.ID)
	if err != nil || !found || unknown.State != domain.WorkflowAttemptStateUnknown {
		t.Fatalf("未形成 UNKNOWN: %+v %v", unknown, err)
	}
	extra.RetireTaskIDs = nil
	input.IdempotencyKey = "boundary-unknown"
	if r := f.asOperator(http.MethodPost, path+"/plan-candidates", m3JSON(t, input)); r.Status != http.StatusConflict {
		t.Fatalf("UNKNOWN 存在时仍生成候选: %d %s", r.Status, r.Raw)
	}
	if r := f.asOperator(http.MethodPost, path+"/plan-candidates/"+prepared.ID+"/confirm", m3JSON(t, confirm)); r.Status != http.StatusConflict {
		t.Fatalf("UNKNOWN 存在时仍确认旧候选: %d %s", r.Status, r.Raw)
	}
	if _, found, err := f.store.GetWorkflowTask(ctx, w, "extra-task"); err != nil || found {
		t.Fatalf("拒绝后仍写入新任务: found=%v err=%v", found, err)
	}
	if len(d.Conversations()) != 2 {
		t.Fatal("失败候选产生额外 Session")
	}
}
