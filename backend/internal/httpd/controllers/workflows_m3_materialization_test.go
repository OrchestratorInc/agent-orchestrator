package controllers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// TestM3AgentRequestMaterializesOnlyAfterSeparateApproval 删除独立审批门禁、许可传递或持久结果重读均会失败。
// 真实 HTTP→principal→collaboration/workflow→SQLite 链证明批准与物化分离，并保留原任务和费用权限。
func TestM3AgentRequestMaterializesOnlyAfterSeparateApproval(t *testing.T) {
	e := newM3ControlEnv(t)
	agent, headers := boundAgentServer(t, e)
	path := "/api/v1/workflows/" + e.workflowID + "/task-requests"
	body := `{"requestId":"m3-request","requestKind":"create_task","payload":{"task":{"kind":"development","title":"Narrow follow-up"},"repositories":[{"repoKey":"backend","allowedPaths":["src/new"]}]}}`
	validFence := e.deps.WorkflowExecutionFence
	e.deps.WorkflowExecutionFence = func(context.Context) (domain.SchedulerFence, error) {
		return domain.SchedulerFence{OwnerID: "foreign-daemon", Epoch: 99}, nil
	}
	foreign := e.serverWith(t, e.deps.WorkflowPrincipals)
	status, raw := doJSONWithHeaders(t, foreign, http.MethodPost, path, body, headers)
	if status != http.StatusConflict || !strings.Contains(string(raw), "SCHEDULER_FENCE_STALE") {
		t.Fatalf("foreign daemon admitted Agent: %d %s", status, raw)
	}
	_, found, err := e.store.GetWorkflowTaskRequest(context.Background(), domain.WorkflowID(e.workflowID), "m3-request")
	if err != nil || found {
		t.Fatalf("foreign daemon wrote request: found=%v err=%v", found, err)
	}
	e.deps.WorkflowExecutionFence = validFence
	status, raw = doJSONWithHeaders(t, agent, http.MethodPost, path, body, headers)
	if status != http.StatusCreated {
		t.Fatalf("agent request: %d %s", status, raw)
	}
	var created controllers.CreateWorkflowTaskRequestResponse
	mustJSON(t, raw, &created)
	if created.Request.RequesterTaskID != e.taskID {
		t.Fatalf("requester=%q", created.Request.RequesterTaskID)
	}
	decidePath := path + "/m3-request/decide"
	decideBody := `{"approve":true,"expectedRevision":1,"reason":"reviewed"}`
	approval := e.createApproval(t, http.MethodPost, decidePath, decideBody)
	e.confirmApproval(t, approval, principal.PayloadDigest([]byte(decideBody)))
	status, raw = e.submitAsApproval(t, http.MethodPost, decidePath, decideBody, approval.Approval.ApprovalID)
	var decision controllers.DecideWorkflowTaskRequestResponse
	mustJSON(t, raw, &decision)
	if status != http.StatusAccepted || decision.Materialized || decision.Materialization != "pending" {
		t.Fatalf("decide: %d %s", status, raw)
	}
	materializePath := path + "/m3-request/materialize"
	materializeBody := `{"requestSha":"` + created.Request.RequestSHA + `","expectedRequestRevision":2,"expectedWorkflowRevision":` + jsonInt(created.Request.WorkflowRevision) + `,"idempotencyKey":"materialize-once","reason":"TEST_REVIEW"}`
	status, raw = e.submitAsApproval(t, http.MethodPost, materializePath, materializeBody, approval.Approval.ApprovalID)
	if status != http.StatusForbidden {
		t.Fatalf("reused decision approval: %d %s", status, raw)
	}
	status, raw = doJSONWithHeaders(t, agent, http.MethodPost, materializePath, materializeBody, headers)
	if status != http.StatusForbidden || !strings.Contains(string(raw), "COLLAB_NOT_AUTHORIZED") {
		t.Fatalf("agent materialize: %d %s", status, raw)
	}
	status, raw = e.approvedAction(t, http.MethodPost, materializePath, materializeBody)
	var first controllers.MaterializeTaskRequestResponse
	mustJSON(t, raw, &first)
	if status != http.StatusCreated || !first.Created {
		t.Fatalf("materialize: %d %s", status, raw)
	}
	status, raw = e.approvedAction(t, http.MethodPost, materializePath, materializeBody)
	var replay controllers.MaterializeTaskRequestResponse
	mustJSON(t, raw, &replay)
	if status != http.StatusOK || replay.Created || replay.Materialization.TaskID != first.Materialization.TaskID {
		t.Fatalf("materialize replay: %d %s", status, raw)
	}
	status, raw = e.approvedAction(t, http.MethodPost, decidePath, decideBody)
	mustJSON(t, raw, &decision)
	if status != http.StatusOK || !decision.Materialized || decision.Materialization != "materialized" {
		t.Fatalf("decision after materialize: %d %s", status, raw)
	}
	tasks, err := e.store.ListWorkflowTasks(context.Background(), domain.WorkflowID(e.workflowID), "", 100, 0)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("task count=%d err=%v", len(tasks), err)
	}
}
