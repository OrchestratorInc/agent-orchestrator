package workflow_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// 删除 Service 必需 Admission 检查会让普通旧 Claim 入口绕过证据；把 replay 放到检查之后会破坏历史查询。
func TestM3ClaimRequiresEvidenceButReplaysHistoricalAdmission(t *testing.T) {
	ctx := context.Background()
	svc, store := newWorkflowService(t)
	wf, _, err := svc.Create(ctx, "m3-create", singleTaskCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Activate(ctx, wf.ID, wf.Revision, "m3-activate"); err != nil {
		t.Fatal(err)
	}
	task, err := svc.GetTask(ctx, wf.ID, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	input := workflow.ClaimInput{WorkflowID: wf.ID, TaskID: task.ID, ExpectedRevision: task.Revision, WorkerID: "worker", IdempotencyKey: "m3-claim"}
	closed := workflow.New(workflow.Deps{Store: store, Clock: workflowServiceTime, LeaseDuration: time.Minute, GraphLimits: domain.WorkflowGraphLimits{MaxTasks: 10, MaxDepth: 5, MaxFanOut: 3}})
	_, _, err = closed.Claim(ctx, input)
	requireAPIError(t, err, "WORKFLOW_ADMISSION_REQUIRED")
	claimed, created, err := svc.Claim(ctx, input)
	if err != nil || !created {
		t.Fatalf("trusted claim: %v %v", created, err)
	}
	replay, created, err := closed.Claim(ctx, input)
	if err != nil || created || replay.Attempt.ID != claimed.Attempt.ID {
		t.Fatalf("historical replay: %v %v", created, err)
	}
	attempts, err := store.ListWorkflowAttempts(ctx, wf.ID, task.ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempt count: %d %v", len(attempts), err)
	}
}
