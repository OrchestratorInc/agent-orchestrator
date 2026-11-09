package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// 删除后代归因或 finalization 检查，会让父预算漏记子任务费用或在用量尚未读完时释放预留。
// 本用例仅为预算层提供已可信终止的执行事实；真实执行释放由 M3 Execution 集成测试验证。
func TestM3BudgetDescendantSettlementWaitsForFinalizedUsage(t *testing.T) {
	s, dir := materializationStoreFixture(t)
	ctx := context.Background()
	now := workflowStoreTime()
	in := approveMaterialization(t, s, dir, "budget-child", "materialization-parent", materializationPayload)
	child, _, err := s.MaterializeWorkflowTaskRequest(ctx, in, materializationLimits)
	mustNoError(t, err)
	policies := []domain.BudgetPolicy{
		putLedgerPolicy(t, s, "global-ledger", "global", "", domain.BudgetObserve, 100, now),
		putLedgerPolicy(t, s, "workflow-ledger", "workflow", string(child.WorkflowID), domain.BudgetObserve, 100, now),
		putLedgerPolicy(t, s, "parent-ledger", "task", "materialization-parent", domain.BudgetObserve, 100, now),
	}
	svc := workflow.New(workflow.Deps{Store: s, Admission: sqlitetest.NewAdmissionFixture(t, s, workflowStoreTime), Clock: workflowStoreTime, LeaseDuration: 15 * time.Minute})
	task, _, err := s.GetWorkflowTask(ctx, child.WorkflowID, child.TaskID)
	mustNoError(t, err)
	claimed, _, err := svc.Claim(ctx, workflow.ClaimInput{WorkflowID: child.WorkflowID, TaskID: child.TaskID, ExpectedRevision: task.Revision, WorkerID: "test-worker", IdempotencyKey: "claim-ledger-child"})
	mustNoError(t, err)
	permit := func() context.Context { return sqlitetest.ExecutionContext(ctx, t, s, claimed.Attempt.ID, now) }
	binding, _, err := s.CreateWorkflowAttemptBinding(permit(), domain.WorkflowAttemptBinding{
		AttemptID: claimed.Attempt.ID, WorkflowID: child.WorkflowID, TaskID: child.TaskID,
		SpawnRequestID: "ledger-child-session", SpawnHash: strings.Repeat("e", 64),
	}, nil, now)
	mustNoError(t, err)
	binding, _, err = s.TransitionWorkflowAttemptBinding(permit(), claimed.Attempt.ID, domain.WorkflowBindingStateSpawnRequested, "", binding.Revision, now)
	mustNoError(t, err)
	admission, _, err := s.GetSchedulerAdmissionForAttempt(ctx, claimed.Attempt.ID)
	mustNoError(t, err)
	seed := domain.SessionRecord{Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		ClientRequestID: binding.SpawnRequestID, ClientRequestHash: binding.SpawnHash, CreatedAt: now, UpdatedAt: now,
		Metadata: domain.SessionMetadata{ControllerGeneration: "ledger-generation"}}
	session, _, err := s.CreateAdmittedSession(permit(), seed, admission, "", now)
	mustNoError(t, err)
	_, _, err = s.BindWorkflowAttemptSession(permit(), claimed.Attempt.ID, session.ID, "ledger-generation", domain.WorkflowBindingStateBoundReady, binding.Revision, now)
	mustNoError(t, err)
	session.IsTerminated = true
	mustNoError(t, s.UpdateSession(ctx, session))
	admission, _, err = s.GetSchedulerAdmissionForAttempt(ctx, claimed.Attempt.ID)
	mustNoError(t, err)
	err = s.ReleaseExecutionAdmission(ctx, admission, ports.SessionObservation{SessionID: session.ID, Terminated: true,
		ControllerGeneration: "ledger-generation", ObservedAt: now}, now)
	mustNoError(t, err)
	source := seedUsageSource(t, s, session, now)
	event := pricedCandidateEvent("child-cost")
	event.CreatedAt, event.Costs = now, ledgerCosts(30, "child-price")
	mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{ByteOffset: 10, State: domain.UsageSourceActive, UpdatedAt: now}, []domain.ModelUsageEvent{event}))
	_, err = s.ReconcileBudgetUsage(ctx, 100, now)
	mustNoError(t, err)
	settled, err := s.ReconcileReleasedBudgetReservations(ctx, 100, now)
	mustNoError(t, err)
	if settled != 0 {
		t.Fatalf("未 finalization 就结算=%d", settled)
	}
	reservations, err := s.ListBudgetReservations(ctx, "")
	mustNoError(t, err)
	if len(reservations) != 3 {
		t.Fatalf("三级预留=%d", len(reservations))
	}
	for _, r := range reservations {
		if r.State != "uncertain" {
			t.Fatalf("终止但费用未完整，预留=%+v", r)
		}
	}
	_, err = s.MarkUsageSourceState(ctx, source.ID, domain.UsageSourceComplete, "", nil, now)
	mustNoError(t, err)
	_, err = s.UpdateUsageBindingState(ctx, source.BindingID, domain.UsageBindingComplete, "", now)
	mustNoError(t, err)
	settled, err = s.ReconcileReleasedBudgetReservations(ctx, 100, now)
	mustNoError(t, err)
	if settled != 1 {
		t.Fatalf("可信完成后未结算=%d", settled)
	}
	for _, p := range policies {
		snapshot, err := s.ReadBudgetSnapshot(ctx, p.ID, now)
		mustNoError(t, err)
		if usageTokenValue(snapshot.CostNanos) != 30 || usageTokenValue(snapshot.ReservedNanos) != 0 || usageTokenValue(snapshot.RemainingNanos) != 70 || snapshot.EventCount != 1 {
			t.Fatalf("%s 未共享同一费用事实: %+v", p.ID, snapshot)
		}
	}
	entries, err := s.ListBudgetUsagePage(ctx, "global", "", 0, 100)
	mustNoError(t, err)
	if len(entries) != 1 || entries[0].TaskID != child.TaskID || entries[0].AttemptID != claimed.Attempt.ID {
		t.Fatalf("三级预算重复成多笔费用或丢失子任务归属: %+v", entries)
	}
	settled, err = s.ReconcileReleasedBudgetReservations(ctx, 100, now)
	mustNoError(t, err)
	if settled != 0 {
		t.Fatalf("重复结算=%d", settled)
	}
}
