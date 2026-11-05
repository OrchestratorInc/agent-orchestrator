package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/budget"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduler"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// 删除服务身份检查、暂停位准入门禁或幂等事务，会允许 Agent 改额或在暂停时启动新 Attempt。
func TestM3ControlOperatorBoundaryAndPauseReplay(t *testing.T) {
	s, _ := materializationStoreFixture(t)
	ctx := context.Background()
	sc := scheduler.New(scheduler.Deps{Store: s, Tasks: s, Clock: workflowStoreTime})
	bc := budget.New(s, workflowStoreTime)
	change := domain.PolicyChange{IdempotencyKey: "control", RequestFingerprint: strings.Repeat("b", 64), Reason: "OPERATOR_CHANGE"}
	agent := principal.Caller{Kind: principal.KindAgent, SessionID: "agent", LeaseProven: true}
	if _, _, err := sc.PutPolicy(ctx, domain.DefaultSchedulerPolicy(""), 1, change, agent); !errors.Is(err, principal.ErrNotAuthorized) {
		t.Fatalf("Agent改并发=%v", err)
	}
	if _, _, err := bc.PutPolicy(ctx, domain.BudgetPolicy{}, 0, change, agent); !errors.Is(err, principal.ErrNotAuthorized) {
		t.Fatalf("Agent改预算=%v", err)
	}
	if _, _, err := sc.SetWorkflowPaused(ctx, "materialization-flow", true, 0, change, principal.Caller{Kind: principal.KindOperator}); !errors.Is(err, principal.ErrNotAuthorized) {
		t.Fatalf("无本次主体授权暂停=%v", err)
	}
	operator := principal.Caller{Kind: principal.KindOperator, OperatorTokenFingerprint: "verified-os-subject"}
	paused, applied, err := sc.SetWorkflowPaused(ctx, "materialization-flow", true, 0, change, operator)
	mustNoError(t, err)
	if !applied || !paused.Paused || paused.Revision != 1 {
		t.Fatalf("暂停=%+v %v", paused, applied)
	}
	f := sqlitetest.NewAdmissionFixture(t, s, workflowStoreTime)
	ws := workflow.New(workflow.Deps{Store: s, Admission: f, Clock: workflowStoreTime, LeaseDuration: 15 * time.Minute})
	task, _, err := s.GetWorkflowTask(ctx, "materialization-flow", "materialization-parent")
	mustNoError(t, err)
	in := workflow.ClaimInput{WorkflowID: task.WorkflowID, TaskID: task.ID, ExpectedRevision: task.Revision, WorkerID: "worker", IdempotencyKey: "paused-claim"}
	if _, applied, err := ws.Claim(ctx, in); applied || !errors.Is(err, domain.ErrSchedulerCapacity) {
		t.Fatalf("暂停仍Claim=%v %v", applied, err)
	}
	resume := change
	resume.IdempotencyKey, resume.RequestFingerprint = "resume", strings.Repeat("c", 64)
	if _, _, err := sc.SetWorkflowPaused(ctx, task.WorkflowID, false, 1, resume, operator); err != nil {
		t.Fatal(err)
	}
	replayed, applied, err := sc.SetWorkflowPaused(ctx, task.WorkflowID, true, 0, change, operator)
	mustNoError(t, err)
	if applied || replayed != paused {
		t.Fatalf("暂停重放=%+v %v", replayed, applied)
	}
	current, err := sc.WorkflowStatus(ctx, task.WorkflowID)
	mustNoError(t, err)
	if current.Paused || current.Revision != 2 {
		t.Fatalf("旧暂停重放改变现态=%+v", current)
	}
	if _, applied, err := ws.Claim(ctx, in); err != nil || !applied {
		t.Fatalf("恢复后Claim=%v %v", applied, err)
	}
}
