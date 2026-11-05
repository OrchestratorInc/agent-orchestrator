package controllers_test

import (
	"context"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestM1WritesRequireOperator 删除 M1 共用 Operator 门禁会让裸回环请求创建图。
func TestM1WritesRequireOperator(t *testing.T) {
	svc := newWorkflowService()
	srv := newWorkflowTestServer(t, svc)
	status, body := doJSONAsAnAnonymousPeer(t, srv, http.MethodPost, "/api/v1/workflows", workflowCreateBody)
	if status != http.StatusForbidden || svc.lastKey != "" {
		t.Fatalf("未审批写入: status=%d calls=%q body=%s", status, svc.lastKey, body)
	}
}

// TestM3RetryNeedsApprovedSchedule 删除重试准入调用或移动到审批之前会错误地重新开放任务。
func TestM3RetryNeedsApprovedSchedule(t *testing.T) {
	var called atomic.Int64
	deps := approvedWorkflowDeps(httpd.APIDeps{Workflows: newWorkflowService(), WorkflowRetrySchedule: func(_ context.Context, wf domain.WorkflowID, task domain.WorkflowTaskID, revision int64) error {
		called.Add(1)
		if wf != "workflow-1" || task != "task-1" || revision != 7 {
			t.Errorf("retry scope: %s %s %d", wf, task, revision)
		}
		return domain.ErrSchedulerFence
	}})
	srv := newControlServer(t, deps)
	path, body := "/api/v1/workflows/workflow-1/tasks/task-1/retry", `{"expectedRevision":7,"idempotencyKey":"retry-1"}`
	status, raw := doJSONWithHeaders(t, srv, http.MethodPost, path, body, nil)
	if status != http.StatusForbidden || called.Load() != 0 {
		t.Fatalf("unapproved retry: %d %s", status, raw)
	}
	status, raw = (&approvalEnv{srv: srv}).approvedAction(t, http.MethodPost, path, body)
	if status != http.StatusConflict || called.Load() != 1 || !strings.Contains(string(raw), "SCHEDULER_FENCE_STALE") {
		t.Fatalf("retry fence: %d %s", status, raw)
	}
	deps.WorkflowRetrySchedule = nil
	status, raw = (&approvalEnv{srv: newControlServer(t, deps)}).approvedAction(t, http.MethodPost, path, body)
	if status != http.StatusNotImplemented {
		t.Fatalf("unwired retry: %d %s", status, raw)
	}
}

// TestEmptyAgentMarkersCannotTriggerConsent 删除 hasOperatorAgentHeaders 的存在性检查会调用原生审批。
func TestEmptyAgentMarkersCannotTriggerConsent(t *testing.T) {
	for _, header := range []string{principal.HeaderSessionID, "X-AO-Workflow-Lease"} {
		t.Run(header, func(t *testing.T) {
			deps := approvedWorkflowDeps(httpd.APIDeps{})
			srv := newControlServer(t, deps)
			env := &approvalEnv{srv: srv}
			body := `{"idempotencyKey":"start"}`
			approval := env.createApproval(t, http.MethodPost, "/api/v1/workflows/w/attempts/a/start", body)
			status, response := doJSONWithHeaders(t, srv, http.MethodPost, "/api/v1/operator/approvals/"+approval.Approval.ApprovalID+"/confirm",
				`{"code":"`+approval.Approval.Code+`","payloadDigest":"`+principal.PayloadDigest([]byte(body))+`"}`, map[string]string{header: ""})
			if status != http.StatusForbidden || len(deps.OperatorConsent.(*recordingConsent).seen()) != 0 {
				t.Fatalf("空Agent标记触发审批: %d %s", status, response)
			}
		})
	}
}
