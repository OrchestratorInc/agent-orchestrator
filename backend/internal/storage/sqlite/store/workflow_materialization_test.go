package store_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

var materializationLimits = domain.WorkflowGraphLimits{MaxTasks: 50, MaxDepth: 12, MaxFanOut: 8}

// materializationStoreFixture 只创建真实图及隔离数据库，不直接写入已批准状态。
func materializationStoreFixture(t *testing.T) (*sqlite.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	seedProject(t, s, "materialization-project")
	now := workflowStoreTime()
	parent := workflowStoreTask("materialization-parent", "materialization-flow", 2, now)
	parent.AgentHarness = domain.HarnessCodex
	_, _, err := s.CreateWorkflow(context.Background(), workflowStoreRecord(parent.WorkflowID, now),
		[]domain.WorkflowRepository{{WorkflowID: parent.WorkflowID, RepoKey: "repo", ProjectID: "materialization-project"}},
		[]domain.WorkflowTask{parent}, nil,
		[]domain.WorkflowTaskRepository{{WorkflowID: parent.WorkflowID, TaskID: parent.ID, RepoKey: "repo", AllowedPaths: []string{"src"}, BaseSHA: "pinned-base"}},
		"materialization-create", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ActivateWorkflow(context.Background(), parent.WorkflowID, 1, "materialization-activate", strings.Repeat("b", 64), now); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

// approveMaterialization 为物化层植入 pending 原件，再经正式 Decide 形成批准；不冒充 Agent 提议链验收。
func approveMaterialization(t *testing.T, s *sqlite.Store, dir, id string, parent domain.WorkflowTaskID, payload string) domain.TaskRequestMaterializationInput {
	t.Helper()
	ctx := context.Background()
	now := workflowStoreTime()
	w, found, err := s.GetWorkflow(ctx, "materialization-flow")
	if err != nil || !found {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(payload))
	db, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO workflow_task_requests(id,workflow_id,requester_task_id,request_kind,request_json,request_sha,state,decided_by_kind,decided_by_ref,decided_at,reason,workflow_revision,revision,created_at,updated_at)
	 VALUES(?,?,?,?,?,?,'pending',NULL,'',NULL,'',?,1,?,?)`, id, w.ID, parent, domain.WorkflowTaskRequestKindCreateTask, payload, hex.EncodeToString(sum[:]), w.Revision, now, now)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.DecideWorkflowTaskRequest(ctx, w.ID, id, domain.WorkflowTaskRequestStateApproved, domain.WorkflowTaskRequestDeciderHuman, "test-os-subject", "approved", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	return domain.TaskRequestMaterializationInput{WorkflowID: w.ID, RequestID: id, RequestSHA: r.RequestSHA,
		ExpectedRequestRevision: r.Revision, ExpectedWorkflowRevision: w.Revision, IdempotencyKey: id + "-materialize",
		RequestFingerprint: r.RequestSHA, ActorRef: "test-operator", Reason: "TASK_REQUEST_MATERIALIZED", Now: now}
}

const materializationPayload = `{"task":{"kind":"testing","title":"验证修改"},"repositories":[{"repoKey":"repo","allowedPaths":["src/tests"]}]}`

// 删除唯一物化/幂等事务会创建重复图；服务与两套真实 Store 共用原件但分别消费本次调用身份。
func TestM3TaskMaterializationSingleResultAcrossStores(t *testing.T) {
	s, dir := materializationStoreFixture(t)
	second, err := sqlite.OpenPreMigrated(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	in := approveMaterialization(t, s, dir, "concurrent-materialization", "materialization-parent", materializationPayload)
	var wg sync.WaitGroup
	start := make(chan struct{})
	var results [2]domain.TaskRequestMaterialization
	var created [2]bool
	var errs [2]error
	for i, store := range []*sqlite.Store{s, second} {
		wg.Add(1)
		go func(i int, store *sqlite.Store) {
			defer wg.Done()
			<-start
			svc := workflow.New(workflow.Deps{Store: store, GraphLimits: materializationLimits, Clock: workflowStoreTime})
			results[i], created[i], errs[i] = svc.MaterializeTaskRequest(context.Background(), in, principal.Caller{Kind: principal.KindOperator, OperatorTokenFingerprint: "test-os-subject"})
		}(i, store)
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || created[0] == created[1] || results[0] != results[1] {
		t.Fatalf("物化竞争没有收敛: created=%v errors=%v results=%+v", created, errs, results)
	}
	tasks, err := s.ListWorkflowTasks(context.Background(), in.WorkflowID, "", 100, 0)
	if err != nil || len(tasks) != 2 || results[0].WorkflowRevision != 3 {
		t.Fatalf("重复物化改变了图: tasks=%d result=%+v err=%v", len(tasks), results[0], err)
	}
	bindings, err := s.ListWorkflowTaskRepositories(context.Background(), in.WorkflowID, results[0].TaskID)
	if err != nil || len(bindings) != 1 || bindings[0].BaseSHA != "pinned-base" || bindings[0].AllowedPaths[0] != "src/tests" {
		t.Fatalf("仓库边界未继承: %+v %v", bindings, err)
	}
	in.IdempotencyKey = "same-request-new-key"
	if replay, created, err := s.MaterializeWorkflowTaskRequest(context.Background(), in, materializationLimits); err != nil || created || replay != results[0] {
		t.Fatalf("原件重复物化未返回原结果: %+v %v %v", replay, created, err)
	}
	in.RequestFingerprint = strings.Repeat("f", 64)
	if _, created, err := s.MaterializeWorkflowTaskRequest(context.Background(), in, materializationLimits); created || !errors.Is(err, domain.ErrWorkflowIdempotencyConflict) {
		t.Fatalf("同一幂等键接纳了不同正文: %v %v", created, err)
	}
}

// 删除事务回滚会在最终物化约束拒绝后留下 Task/Edge/Repo 或增加图 revision。
func TestM3TaskMaterializationRollsBackTheWholeGraph(t *testing.T) {
	s, dir := materializationStoreFixture(t)
	payload := `{"task":{"kind":"testing","title":"验证修改"},"dependsOn":["materialization-parent"],"repositories":[{"repoKey":"repo","allowedPaths":["src/tests"]}]}`
	in := approveMaterialization(t, s, dir, "atomic-materialization", "materialization-parent", payload)
	db, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER reject_materialization BEFORE INSERT ON workflow_task_materializations BEGIN SELECT RAISE(ABORT,'test constraint'); END`); err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.MaterializeWorkflowTaskRequest(context.Background(), in, materializationLimits); err == nil || created {
		t.Fatalf("最终约束没有阻断事务: created=%v err=%v", created, err)
	}
	tasks, err := s.ListWorkflowTasks(context.Background(), in.WorkflowID, "", 100, 0)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("失败后遗留半图: tasks=%d err=%v", len(tasks), err)
	}
	edges, err := s.ListWorkflowEdges(context.Background(), in.WorkflowID)
	if err != nil || len(edges) != 0 {
		t.Fatalf("失败后遗留依赖: %+v %v", edges, err)
	}
	w, _, err := s.GetWorkflow(context.Background(), in.WorkflowID)
	if err != nil || w.Revision != in.ExpectedWorkflowRevision {
		t.Fatalf("失败仍修改图 revision: %+v %v", w, err)
	}
	if _, found, err := s.GetWorkflowTaskMaterialization(context.Background(), in.WorkflowID, in.RequestID); err != nil || found {
		t.Fatalf("失败仍有物化结果: %v %v", found, err)
	}
}

// 删除祖先政策选择或共享 PolicyID，会让孙任务在父池已被子任务预留后继续占用预算。
func TestM3MaterializedDescendantsShareAncestorBudget(t *testing.T) {
	s, dir := materializationStoreFixture(t)
	ctx := context.Background()
	childIn := approveMaterialization(t, s, dir, "child", "materialization-parent", materializationPayload)
	child, _, err := s.MaterializeWorkflowTaskRequest(ctx, childIn, materializationLimits)
	if err != nil {
		t.Fatal(err)
	}
	grandIn := approveMaterialization(t, s, dir, "grandchild", child.TaskID, materializationPayload)
	grandchild, _, err := s.MaterializeWorkflowTaskRequest(ctx, grandIn, materializationLimits)
	if err != nil {
		t.Fatal(err)
	}
	n := int64(1)
	p := domain.BudgetPolicy{ID: "parent-budget", Scope: "task", ScopeID: "materialization-parent", Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime,
		LimitNanos: 1, ReservationNanos: &n, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "approved-bound"}
	if _, _, err := s.PutBudgetPolicy(ctx, p, 0, domain.PolicyChange{IdempotencyKey: "parent-budget", RequestFingerprint: strings.Repeat("e", 64), ActorRef: "test-operator", Reason: "TASK_BUDGET", Now: workflowStoreTime()}); err != nil {
		t.Fatal(err)
	}
	svc := workflow.New(workflow.Deps{Store: s, Admission: sqlitetest.NewAdmissionFixture(t, s, workflowStoreTime), Clock: workflowStoreTime, LeaseDuration: 15 * time.Minute})
	for i, result := range []domain.TaskRequestMaterialization{child, grandchild} {
		task, _, err := s.GetWorkflowTask(ctx, result.WorkflowID, result.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		_, created, err := svc.Claim(ctx, workflow.ClaimInput{WorkflowID: task.WorkflowID, TaskID: task.ID, ExpectedRevision: task.Revision, WorkerID: "test-worker", IdempotencyKey: string(task.ID) + "-claim"})
		if i == 0 && (err != nil || !created) || i == 1 && (!errors.Is(err, domain.ErrBudgetExceeded) || created) {
			t.Fatalf("共享父预算未限制第%d个派生任务: created=%v err=%v", i+1, created, err)
		}
	}
	reservations, err := s.ListBudgetReservations(ctx, "")
	if err != nil || len(reservations) != 1 || reservations[0].PolicyID != p.ID {
		t.Fatalf("子任务没有共享原父策略: %+v %v", reservations, err)
	}
}
