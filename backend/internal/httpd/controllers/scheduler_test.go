package controllers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// TestM3SchedulerControlApprovalAndCAS 删除 Operator 门禁、原始正文指纹或暂停方向分支均会失败。
func TestM3SchedulerControlApprovalAndCAS(t *testing.T) {
	e := newM3ControlEnv(t)
	status, raw := doJSONWithHeaders(t, e.srv, http.MethodGet, "/api/v1/scheduler/status", "", nil)
	var snapshot controllers.SchedulerStatusResponse
	mustJSON(t, raw, &snapshot)
	if status != http.StatusOK || len(snapshot.Policies) == 0 {
		t.Fatalf("status: %d %s", status, raw)
	}
	var p controllers.SchedulerPolicyResponse
	for _, row := range snapshot.Policies {
		if row.Scope == "global" {
			p = row
		}
	}
	policy := controllers.PutSchedulerPolicyRequest{OperatorChangeRequest: controllers.OperatorChangeRequest{ExpectedRevision: p.Revision, IdempotencyKey: "policy-1", Reason: "TEST_REVIEW"}, MaxConcurrent: 4,
		CPURecoverPercent: p.CPURecoverPercent, CPUSoftPercent: p.CPUSoftPercent, CPUHardPercent: p.CPUHardPercent, RAMSoftPercent: p.RAMSoftPercent, RAMHardPercent: p.RAMHardPercent, RAMFloorBytes: p.RAMFloorBytes}
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	body, path := string(encoded), "/api/v1/scheduler/policy"
	status, raw = doJSONWithHeaders(t, e.srv, http.MethodPut, path, body, nil)
	if status != http.StatusForbidden {
		t.Fatalf("unapproved policy: %d %s", status, raw)
	}
	approval := e.createApproval(t, http.MethodPut, path, body)
	e.confirmApproval(t, approval, principal.PayloadDigest(encoded))
	status, raw = e.submitAsApproval(t, http.MethodPut, path, body, approval.Approval.ApprovalID)
	var changed controllers.SchedulerPolicyEnvelope
	mustJSON(t, raw, &changed)
	if status != http.StatusOK || !changed.Applied || changed.Policy.MaxConcurrent != 4 || changed.Policy.Revision != p.Revision+1 {
		t.Fatalf("policy write: %d %s", status, raw)
	}
	status, raw = e.submitAsApproval(t, http.MethodPut, path, body, approval.Approval.ApprovalID)
	if status != http.StatusForbidden {
		t.Fatalf("approval replay: %d %s", status, raw)
	}
	status, raw = e.approvedAction(t, http.MethodPut, path, body)
	mustJSON(t, raw, &changed)
	if status != http.StatusOK || changed.Applied || changed.Policy.Revision != p.Revision+1 {
		t.Fatalf("idempotent policy replay: %d %s", status, raw)
	}
	policy.IdempotencyKey = "policy-conflict"
	encoded, _ = json.Marshal(policy)
	status, raw = e.approvedAction(t, http.MethodPut, path, string(encoded))
	if status != http.StatusConflict {
		t.Fatalf("stale policy revision: %d %s", status, raw)
	}
	for index, verb := range []string{"pause", "resume-scheduling"} {
		body = `{"expectedRevision":` + jsonInt(int64(index)) + `,"idempotencyKey":"` + verb + `","reason":"TEST_REVIEW"}`
		status, raw = e.approvedAction(t, http.MethodPost, "/api/v1/workflows/"+e.workflowID+"/"+verb, body)
		var state controllers.WorkflowSchedulerResponse
		mustJSON(t, raw, &state)
		if status != http.StatusOK || state.Paused != (index == 0) || state.Revision != int64(index+1) {
			t.Fatalf("%s: %d %s", verb, status, raw)
		}
	}
}

// TestM3BudgetScopeComesOnlyFromRoute 删除严格解析或 Task 所属校验会写入错误作用域。
// 使用真实三级策略和读回，确保 TaskID 不是 Workflow/Task 拼接值。
func TestM3BudgetScopeComesOnlyFromRoute(t *testing.T) {
	e := newM3ControlEnv(t)
	for _, scope := range []struct{ path, kind, id string }{
		{"/api/v1/scheduler/budget", "global", ""},
		{"/api/v1/workflows/" + e.workflowID + "/budget", "workflow", e.workflowID},
		{"/api/v1/workflows/" + e.workflowID + "/tasks/" + e.taskID + "/budget", "task", e.taskID},
	} {
		body := `{"id":"budget-` + scope.kind + `","currency":"USD","mode":"observe","period":"lifetime","limitNanos":10000,"expectedRevision":0,"idempotencyKey":"budget-` + scope.kind + `","reason":"TEST_REVIEW"}`
		status, raw := e.approvedAction(t, http.MethodPut, scope.path, body)
		var changed controllers.BudgetPolicyEnvelope
		mustJSON(t, raw, &changed)
		if status != http.StatusOK || !changed.Applied || changed.Policy.Scope != scope.kind || changed.Policy.ScopeID != scope.id {
			t.Fatalf("budget %s: %d %s", scope.kind, status, raw)
		}
		status, raw = doJSONWithHeaders(t, e.srv, http.MethodGet, scope.path, "", nil)
		var read controllers.BudgetSnapshotsResponse
		mustJSON(t, raw, &read)
		if status != http.StatusOK || len(read.Policies) != 1 || len(read.Snapshots) != 1 || read.Policies[0].ID != changed.Policy.ID {
			t.Fatalf("budget read %s: %d %s", scope.kind, status, raw)
		}
	}
	body := `{"id":"escape","currency":"USD","mode":"observe","period":"lifetime","limitNanos":1,"expectedRevision":0,"idempotencyKey":"escape","reason":"TEST_REVIEW","scope":"global"}`
	status, raw := e.approvedAction(t, http.MethodPut, "/api/v1/workflows/"+e.workflowID+"/tasks/"+e.taskID+"/budget", body)
	if status != http.StatusBadRequest {
		t.Fatalf("scope injection: %d %s", status, raw)
	}
	body = `{"id":"foreign","currency":"USD","mode":"observe","period":"lifetime","limitNanos":1,"expectedRevision":0,"idempotencyKey":"foreign","reason":"TEST_REVIEW"}`
	status, raw = e.approvedAction(t, http.MethodPut, "/api/v1/workflows/foreign/tasks/"+e.taskID+"/budget", body)
	if status != http.StatusNotFound {
		t.Fatalf("foreign task: %d %s", status, raw)
	}
}
