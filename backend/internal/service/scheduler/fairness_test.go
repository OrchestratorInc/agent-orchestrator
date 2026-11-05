package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// 删除 Workflow 分页或预算拒绝后游标推进，大量拒绝任务会遮住 B 的可运行任务。
func TestBudgetBlockedLargeWorkflowDoesNotStarveOthers(t *testing.T) {
	f := newFixture(t)
	a := f.create(150, domain.HarnessCodex)
	b := f.create(2, domain.HarnessCodex)
	amount := int64(1)
	p := domain.BudgetPolicy{ID: "budget:blocked", Scope: "workflow", ScopeID: string(a.ID), Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime,
		LimitNanos: 0, ReservationNanos: &amount, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "approval-1"}
	if _, ok, err := f.store.PutBudgetPolicy(context.Background(), p, 0, f.change(p.ID)); err != nil || !ok {
		t.Fatalf("budget: %v %v", ok, err)
	}
	out := f.tick()
	if len(out.Dispatches) != 1 || out.Dispatches[0].WorkflowID != b.ID || out.Examined > 6 {
		t.Fatalf("budget fairness: %+v", out)
	}
	if len(out.Decisions) != 5 {
		t.Fatalf("expected bounded five denials: %+v", out.Decisions)
	}
	for _, d := range out.Decisions {
		if d.ReasonCode != "BUDGET_EXCEEDED" || d.PolicyRevisions[p.RevisionKey()] != 1 {
			t.Fatalf("decision: %+v", d)
		}
	}
}

// 删除保留未消费 deficit 的分支，三条批次上限会把 5:1 权重压成错误的 3:1。
func TestWeightedDeficitContinuesAcrossTicks(t *testing.T) {
	f := newFixture(t)
	f.policy("", 10)
	f.policy(domain.HarnessCodex, 10)
	a := f.create(12, domain.HarnessCodex)
	b := f.create(12, domain.HarnessCodex)
	l, _ := f.store.GetSchedulerLeader(context.Background())
	if ok, err := f.store.PutSchedulerWorkflowState(context.Background(), domain.SchedulerWorkflowState{WorkflowID: a.ID, Weight: 5}, 0, l.Fence, f.clock.Now()); err != nil || !ok {
		t.Fatal(err)
	}
	counts := map[domain.WorkflowID]int{}
	for i := 0; i < 2; i++ {
		for _, d := range f.tick().Dispatches {
			counts[d.WorkflowID]++
		}
	}
	if counts[a.ID] != 5 || counts[b.ID] != 1 {
		t.Fatalf("weighted allocation=%v", counts)
	}
	states, err := f.store.ListSchedulerWorkflowStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.Deficit != 0 {
			t.Fatalf("unconsumed state=%+v", state)
		}
	}
}

// 删除 Unknown Harness 闭锁会让空配置任务偷偷选默认供应商。
func TestUnconfiguredHarnessAndUnknownBudgetStayUnknown(t *testing.T) {
	f := newFixture(t)
	w := f.create(1, "")
	task := f.tasks(w)[0]
	if _, err := f.scheduler.PrepareWorkflowAdmission(context.Background(), task); !errors.Is(err, domain.ErrCapabilityEvidenceInsufficient) {
		t.Fatalf("empty harness=%v", err)
	}
	f.scheduler.deps.ResolveHarness = func(context.Context, domain.WorkflowTask) (domain.AgentHarness, error) {
		return domain.HarnessCodex, nil
	}
	e, err := f.scheduler.PrepareWorkflowAdmission(context.Background(), task)
	if err != nil || e.Capability.Harness != domain.HarnessCodex || e.ReservationNanos != nil {
		t.Fatalf("trusted harness/unknown amount: %+v %v", e, err)
	}
	p := domain.BudgetPolicy{ID: "budget:unknown", Scope: "global", Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime, LimitNanos: 100}
	if _, ok, err := f.store.PutBudgetPolicy(context.Background(), p, 0, f.change(p.ID)); err != nil || !ok {
		t.Fatal(err)
	}
	if out := f.tick(); len(out.Dispatches) != 0 || len(out.Decisions) != 1 || out.Decisions[0].ReasonCode != "BUDGET_UNKNOWN" {
		t.Fatalf("unknown budget=%+v", out)
	}
}

type failedCandidates struct{ CandidateStore }

func (failedCandidates) ListSchedulerWorkflows(context.Context, domain.WorkflowID, int64) ([]domain.Workflow, error) {
	return nil, errors.New("candidate read failed")
}

// 删除底层读取错误传播会把无法观测队列伪装为正常空闲。
func TestCandidateStorageFailureStopsTheRound(t *testing.T) {
	f := newFixture(t)
	f.scheduler.deps.Tasks = failedCandidates{f.store}
	if out, err := f.scheduler.Tick(context.Background()); err == nil || len(out.Dispatches) != 0 {
		t.Fatalf("read failure hidden: %+v %v", out, err)
	}
}

// 删除 DAG 硬门禁或使用未老化的优先级，阻塞任务会提前执行或久等任务会被插队。
func TestDAGReadinessAndAgingOrder(t *testing.T) {
	f := newFixture(t)
	f.policy("", 10)
	f.policy(domain.HarnessCodex, 10)
	w, _, err := f.workflow.Create(context.Background(), "aging-create", workflow.CreateInput{Title: "Aging", Goal: "DAG before priority",
		Repositories: []workflow.RepositoryInput{{RepoKey: "repo", ProjectID: "project"}}, Tasks: []workflow.TaskInput{
			{ID: "prerequisite", Title: "Prerequisite", Kind: domain.WorkflowTaskKindDevelopment, AgentHarness: domain.HarnessCodex, Priority: 10},
			{ID: "old-ready", Title: "Old", Kind: domain.WorkflowTaskKindDevelopment, AgentHarness: domain.HarnessCodex},
			{ID: "new-ready", Title: "New", Kind: domain.WorkflowTaskKindDevelopment, AgentHarness: domain.HarnessCodex, Priority: 1},
		}, Dependencies: []workflow.DependencyInput{{FromTaskID: "prerequisite", ToTaskID: "new-ready"}}})
	if err != nil {
		t.Fatal(err)
	}
	w, _, err = f.workflow.Activate(context.Background(), w.ID, w.Revision, "aging-activate")
	if err != nil {
		t.Fatal(err)
	}
	blocked, _, err := f.store.GetWorkflowTask(context.Background(), w.ID, "new-ready")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.scheduler.PrepareWorkflowAdmission(context.Background(), blocked); !errors.Is(err, domain.ErrWorkflowTaskNotClaimable) {
		t.Fatalf("DAG gate: %v", err)
	}
	first := f.tick()
	if len(first.Dispatches) != 1 || first.Dispatches[0].TaskID != "prerequisite" {
		t.Fatalf("priority: %+v", first)
	}
	a, found, err := f.store.GetWorkflowAttempt(context.Background(), first.Dispatches[0].AttemptID)
	if err != nil || !found {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		f.clock.Advance(20 * time.Second)
		if _, err := f.scheduler.MaintainLeadership(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := f.workflow.Settle(f.permit(a.ID), workflow.SettleInput{WorkflowID: w.ID, AttemptID: a.ID, LeaseToken: a.LeaseToken, Result: domain.WorkflowAttemptStateSucceeded, IdempotencyKey: "prerequisite-complete"}); err != nil {
		t.Fatal(err)
	}
	next := f.tick()
	if len(next.Dispatches) != 1 || next.Dispatches[0].TaskID != "old-ready" {
		t.Fatalf("aging order: %+v", next)
	}
}

// 删除持久分页会永远看不到第六项；删除短页归零会错过随后刚就绪的高优先级任务。
func TestBlockedPrefixEventuallyYieldsAndTailWrapsToNewHead(t *testing.T) {
	f := newFixture(t)
	in := workflow.CreateInput{Title: "Blocked prefix", Goal: "Inspect every eligible task", Repositories: []workflow.RepositoryInput{{RepoKey: "repo", ProjectID: "project"}}}
	for i := 0; i < 5; i++ {
		in.Tasks = append(in.Tasks, workflow.TaskInput{ID: domain.WorkflowTaskID(fmt.Sprintf("blocked-%d", i)), Title: "Budget blocked", Kind: domain.WorkflowTaskKindDevelopment, AgentHarness: domain.HarnessCodex})
	}
	in.Tasks = append(in.Tasks, workflow.TaskInput{ID: "eligible-sixth", Title: "Eligible", Kind: domain.WorkflowTaskKindDevelopment, AgentHarness: domain.HarnessCodex, Priority: -1},
		workflow.TaskInput{ID: "new-head", Title: "New head", Kind: domain.WorkflowTaskKindDevelopment, AgentHarness: domain.HarnessCodex, Priority: 100})
	in.Dependencies = []workflow.DependencyInput{{FromTaskID: "eligible-sixth", ToTaskID: "new-head"}}
	w, _, err := f.workflow.Create(context.Background(), "blocked-prefix", in)
	if err != nil {
		t.Fatal(err)
	}
	w, _, err = f.workflow.Activate(context.Background(), w.ID, w.Revision, "blocked-prefix-activate")
	if err != nil {
		t.Fatal(err)
	}
	amount := int64(1)
	for i := 0; i < 5; i++ {
		p := domain.BudgetPolicy{ID: fmt.Sprintf("prefix-%d", i), Scope: "task", ScopeID: fmt.Sprintf("blocked-%d", i), Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime, LimitNanos: 0,
			ReservationNanos: &amount, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "test-approval"}
		if _, _, err := f.store.PutBudgetPolicy(context.Background(), p, 0, f.change(p.ID)); err != nil {
			t.Fatal(err)
		}
	}
	first := f.tick()
	if first.Examined != 5 || len(first.Dispatches) != 0 {
		t.Fatalf("blocked prefix: %+v", first)
	}
	states, err := f.store.ListSchedulerWorkflowStates(context.Background())
	if err != nil || len(states) != 1 || states[0].CandidateOffset != 5 {
		t.Fatalf("persisted page: %+v %v", states, err)
	}
	f.clock.Advance(LeaderLease + time.Second)
	f.connect("prefix-restart")
	f.recover()
	second := f.tick()
	if len(second.Dispatches) != 1 || second.Dispatches[0].TaskID != "eligible-sixth" {
		t.Fatalf("sixth not selected: %+v", second)
	}
	a, found, err := f.store.GetWorkflowAttempt(context.Background(), second.Dispatches[0].AttemptID)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, _, err := f.workflow.Settle(f.permit(a.ID), workflow.SettleInput{WorkflowID: w.ID, AttemptID: a.ID, LeaseToken: a.LeaseToken, Result: domain.WorkflowAttemptStateSucceeded, IdempotencyKey: "prefix-complete"}); err != nil {
		t.Fatal(err)
	}
	third := f.tick()
	if len(third.Dispatches) != 1 || third.Dispatches[0].TaskID != "new-head" {
		t.Fatalf("tail did not wrap to newly eligible high priority: %+v", third)
	}
}
