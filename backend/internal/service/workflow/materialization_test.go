package workflow_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// materializationProbe 只观测授权后送到持久化边界的值，不替代后续真实 SQLite 图事务测试。
type materializationProbe struct {
	workflow.Store
	calls int
	input domain.TaskRequestMaterializationInput
}

func (p *materializationProbe) MaterializeWorkflowTaskRequest(_ context.Context, input domain.TaskRequestMaterializationInput, _ domain.WorkflowGraphLimits) (domain.TaskRequestMaterialization, bool, error) {
	p.calls++
	p.input = input
	return domain.TaskRequestMaterialization{RequestID: input.RequestID, WorkflowID: input.WorkflowID, TaskID: "request-approved"}, true, nil
}

// 删除 Caller 门禁将允许 Agent 到达持久化；删除服务端 Actor/Now 赋值会保留伪造的审计身份。
func TestTaskMaterializationRequiresCurrentOperatorIdentity(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
	store := &materializationProbe{}
	svc := workflow.New(workflow.Deps{Store: store, Clock: func() time.Time { return now },
		GraphLimits: domain.WorkflowGraphLimits{MaxTasks: 50, MaxDepth: 12, MaxFanOut: 8}})
	input := domain.TaskRequestMaterializationInput{WorkflowID: "flow", RequestID: "approved",
		RequestSHA: strings.Repeat("a", 64), RequestFingerprint: strings.Repeat("b", 64),
		ExpectedRequestRevision: 2, ExpectedWorkflowRevision: 3, IdempotencyKey: "materialize",
		ActorRef: "forged", Reason: "TASK_REQUEST_MATERIALIZED", Now: now.Add(time.Hour)}
	_, _, err := svc.MaterializeTaskRequest(ctx, input, principal.Caller{Kind: principal.KindAgent, SessionID: "agent", AttemptID: "attempt", LeaseProven: true})
	var refusal *apierr.Error
	if !errors.As(err, &refusal) || refusal.Code != "COLLAB_NOT_AUTHORIZED" || store.calls != 0 {
		t.Fatalf("Agent 物化未在持久化前拒绝: err=%v calls=%d", err, store.calls)
	}
	result, created, err := svc.MaterializeTaskRequest(ctx, input, principal.Caller{Kind: principal.KindOperator, OperatorTokenFingerprint: "test-os-subject"})
	if err != nil || !created || result.TaskID != "request-approved" || store.calls != 1 {
		t.Fatalf("本次 Operator 身份未到达持久化: result=%+v created=%v err=%v", result, created, err)
	}
	if store.input.ActorRef != "operator:operator:test-os-subject" || !store.input.Now.Equal(now) {
		t.Fatalf("物化使用了调用方提供的审计身份或时钟: %+v", store.input)
	}
}
