package integration

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// TestM5PlanningCapacityOne 从目标经真实 Chat 输出到新任务，再可信停止并同 Workflow 重规划。
// 去掉任一物理停止、路径冻结释放、READY 转换或服务层幂等前置读取，后续容量 1 准入即失败。
func TestM5PlanningCapacityOne(t *testing.T) {
	f, d := newM5Fixture(t)
	ctx := context.Background()
	sha := strings.TrimSpace(m2smGit(t, f.repoA, "rev-parse", "HEAD"))
	created := m4HTTP[controllers.WorkflowEnvelope](t, f, http.MethodPost, "/workflows/plan", controllers.StartWorkflowPlanRequest{
		Title: "离线目标规划", Goal: "为已选仓库新增一个经过审核的功能", IdempotencyKey: "m5-plan", AgentHarness: "codex", Model: "planner-model", Effort: "high",
		Repositories: []controllers.WorkflowRepositoryInput{{RepoKey: "repo-a", ProjectID: string(f.projectA), BaseSHA: sha, TargetRef: "refs/heads/main"}},
	})
	w := domain.WorkflowID(created.Workflow.ID)
	path := "/workflows/" + string(w)
	m4HTTP[controllers.WorkflowEnvelope](t, f, http.MethodPost, path+"/activate", map[string]any{"expectedRevision": created.Workflow.Revision, "idempotencyKey": "m5-activate"})
	planner := m5Dispatch(t, f)
	m5Reply(t, f, d, planner, m5Patch(f, "worker-1"))
	m5VerifyReadAccess(t, f, planner)
	d.mu.Lock()
	cfg, prompt := d.starts[0], d.prompts[0]
	d.mu.Unlock()
	if !cfg.ReadOnly || cfg.Model != "planner-model" || cfg.Effort != "high" || len(cfg.MCPServers) != 0 || !strings.Contains(prompt, "经过审核的功能") {
		t.Fatalf("Planner 设置/目标未真实送达: %+v %q", cfg, prompt)
	}
	def, err := f.m3.workflows.GetDefinition(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	prepare := workflow.PlanInput{SourceAttemptID: planner.ID, ExpectedRevision: def.Workflow.Revision, IdempotencyKey: "m5-candidate"}
	candidate := m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, path+"/plan-candidates", prepare).Candidate
	prepare.IdempotencyKey = "m5-rival-candidate"
	rival := m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, path+"/plan-candidates", prepare).Candidate
	cycle := m5Patch(f, "cycle-a")
	other := m5Patch(f, "cycle-b")
	cycle.Tasks = append(cycle.Tasks, other.Tasks...)
	cycle.TaskRepositories = append(cycle.TaskRepositories, other.TaskRepositories...)
	cycle.Dependencies = []workflow.DependencyInput{{FromTaskID: "cycle-a", ToTaskID: "cycle-b"}, {FromTaskID: "cycle-b", ToTaskID: "cycle-a"}}
	prepare.Edited, prepare.IdempotencyKey = &cycle, "m5-cycle"
	if r := f.asOperator(http.MethodPost, path+"/plan-candidates", m3JSON(t, prepare)); r.Status != http.StatusBadRequest {
		t.Fatalf("循环候选未拒绝: %d %s", r.Status, r.Raw)
	}
	invalid := m5Patch(f, "bad-scope")
	invalid.TaskRepositories[0].RepoKey = "unselected"
	prepare.Edited = &invalid
	prepare.IdempotencyKey = "m5-bad-candidate"
	if r := f.asOperator(http.MethodPost, path+"/plan-candidates", m3JSON(t, prepare)); r.Status != http.StatusBadRequest {
		t.Fatalf("越界候选未拒绝: %d %s", r.Status, r.Raw)
	}
	confirm := controllers.ConfirmWorkflowPlanRequest{Digest: candidate.Digest, ExpectedRevision: candidate.ExpectedRevision}
	confirmed := m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, path+"/plan-candidates/"+candidate.ID+"/confirm", confirm).Candidate
	replayed := m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, path+"/plan-candidates/"+candidate.ID+"/confirm", confirm).Candidate
	if confirmed.ConfirmedRevision != replayed.ConfirmedRevision {
		t.Fatal("确认重放增加了图版本")
	}
	if r := f.asOperator(http.MethodPost, path+"/plan-candidates/"+rival.ID+"/confirm", m3JSON(t, confirm)); r.Status != http.StatusConflict {
		t.Fatalf("图版本漂移后旧候选仍被确认: %d %s", r.Status, r.Raw)
	}
	if out, e := f.m3.scheduler.Tick(ctx); e != nil || len(out.Dispatches) != 0 {
		t.Fatalf("确认后未保持暂停: %+v %v", out, e)
	}
	before, err := f.store.ListSessionWorktrees(ctx, *planner.SessionID)
	if err != nil || len(before) != 1 {
		t.Fatalf("planner worktree: %+v %v", before, err)
	}
	f = m5Restart(t, f, d)
	m5Retire(t, f)
	m5Resume(t, f, w)
	worker := m5Dispatch(t, f)
	if worker.TaskID != "worker-1" || worker.AttemptNo != 1 {
		t.Fatalf("新任务准入错误: %+v", worker)
	}
	d.mu.Lock()
	workerCfg := d.starts[1]
	d.mu.Unlock()
	if workerCfg.ReadOnly || workerCfg.Model != "worker-model" || workerCfg.Effort != "high" {
		t.Fatalf("开发模型/effort 发生静默替换: %+v", workerCfg)
	}
	if _, err := os.Stat(before[0].WorktreePath); err != nil {
		t.Fatalf("旧 Planner 工作树丢失: %v", err)
	}
	m5VerifyQueue(t, f, d, worker)
	stop := m4HTTP[controllers.WorkflowInterventionEnvelope](t, f, http.MethodPost, f.attemptPath(w, worker.ID, "interventions"), map[string]any{"mode": "stop", "clientMessageId": "m5-stop"})
	if stop.Intervention.State != string(domain.WorkflowInterventionStateStopConfirmed) {
		t.Fatalf("停止未确认: %+v", stop)
	}
	receipts := m4HTTP[controllers.WorkflowInterventionReceiptsResponse](t, f, http.MethodGet, f.attemptPath(w, worker.ID, "interventions"), nil)
	if !slices.ContainsFunc(receipts.Interventions, func(row controllers.WorkflowInterventionResponse) bool {
		return row.ID == stop.Intervention.ID && row.State == stop.Intervention.State
	}) {
		t.Fatalf("只读回执未保留原停止事实: %+v", receipts)
	}
	def, err = f.m3.workflows.GetDefinition(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	replan := workflow.ReplanInput{StopInterventionID: stop.Intervention.ID, ExpectedRevision: def.Workflow.Revision, IdempotencyKey: "m5-replan", AgentHarness: domain.HarnessCodex, Model: "planner-model", Effort: "high"}
	newTask := m4HTTP[controllers.WorkflowTaskEnvelope](t, f, http.MethodPost, path+"/replan", replan).Task
	// 丢回执后原样重放经过 HTTP/Service，不允许绕过服务前置校验只测 Store。
	again := m4HTTP[controllers.WorkflowTaskEnvelope](t, f, http.MethodPost, path+"/replan", replan).Task
	if newTask.ID != again.ID {
		t.Fatalf("重放新建 Planner: %s %s", newTask.ID, again.ID)
	}
	changed := replan
	changed.Model = "changed-model"
	if r := f.asOperator(http.MethodPost, path+"/replan", m3JSON(t, changed)); r.Status != http.StatusConflict {
		t.Fatalf("同键改正文未冲突: %d %s", r.Status, r.Raw)
	}
	if out, e := f.m3.scheduler.Tick(ctx); e != nil || len(out.Dispatches) != 0 {
		t.Fatalf("旧容量尚未收尾却新准入: %+v %v", out, e)
	}
	m5Retire(t, f)
	replanner := m5Dispatch(t, f)
	if string(replanner.TaskID) != newTask.ID || replanner.ID == planner.ID {
		t.Fatal("重规划未产生独立 Planner Attempt")
	}
	m5Reply(t, f, d, replanner, m5Patch(f, "worker-2", worker.TaskID))
	def, err = f.m3.workflows.GetDefinition(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	oldTurn := workflow.PlanInput{SourceAttemptID: replanner.ID, SourceTurnID: confirmed.SourceTurnID, ExpectedRevision: def.Workflow.Revision, IdempotencyKey: "m5-old-turn"}
	if r := f.asOperator(http.MethodPost, path+"/plan-candidates", m3JSON(t, oldTurn)); r.Status != http.StatusConflict {
		t.Fatalf("停止前旧 Planner 输出被重规划采纳: %d %s", r.Status, r.Raw)
	}
	candidate = m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, path+"/plan-candidates", workflow.PlanInput{SourceAttemptID: replanner.ID, ExpectedRevision: def.Workflow.Revision, IdempotencyKey: "m5-recandidate"}).Candidate
	if candidate.SourceTurnID == confirmed.SourceTurnID || candidate.StopInterventionID != stop.Intervention.ID {
		t.Fatal("重规划错误复用停止前输出")
	}
	m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, path+"/plan-candidates/"+candidate.ID+"/confirm", controllers.ConfirmWorkflowPlanRequest{Digest: candidate.Digest, ExpectedRevision: candidate.ExpectedRevision})
	m5Retire(t, f)
	m5Resume(t, f, w)
	next := m5Dispatch(t, f)
	if next.TaskID != "worker-2" || len(d.Conversations()) != 4 {
		t.Fatalf("重规划重复 Spawn: task=%s conversations=%d", next.TaskID, len(d.Conversations()))
	}
	if out, e := f.m3.scheduler.Tick(ctx); e != nil || len(out.Dispatches) != 0 {
		t.Fatalf("重复 Tick 多次准入: %+v %v", out, e)
	}
	old, found, err := f.store.GetWorkflowTask(ctx, w, worker.TaskID)
	if err != nil || !found || old.State != domain.WorkflowTaskStateCancelled {
		t.Fatalf("旧任务未保留为取消: %+v %v", old, err)
	}
}
