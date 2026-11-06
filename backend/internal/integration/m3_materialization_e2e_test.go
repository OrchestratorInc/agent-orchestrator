package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// TestM3AgentRequestRequiresIndependentMaterialization 删除 Operator 门禁、原件SHA或唯一物化事务会允许越权或重复建任务。
func TestM3AgentRequestRequiresIndependentMaterialization(t *testing.T) {
	f := newM3Fixture(t)
	wid := m3Create(t, f, "request", 1)
	dispatched := m3Dispatch(t, f)
	if len(dispatched) != 1 {
		t.Fatalf("dispatches=%d", len(dispatched))
	}
	aid := dispatched[0].AttemptID
	sid := *f.binding(aid).SessionID
	agent := f.asAgent(f.issueAgentCapability(sid), "", aid)
	requestPath := "/workflows/" + string(wid) + "/task-requests"
	// 子任务保持夹具已授予的父任务范围，不能隐式申请整仓权限。
	requestBody := fmt.Sprintf(`{"requestId":"request-new","requestKind":"create_task","payload":{"task":{"kind":"development","title":"review follow-up"},"repositories":[{"repoKey":"repo-a","allowedPaths":[%q]}]}}`, string(f.binding(aid).TaskID))
	raised := f.do(http.MethodPost, requestPath, requestBody, agent)
	if raised.Status != http.StatusCreated {
		t.Fatalf("request: %d %s", raised.Status, raised.Raw)
	}
	id := f.text(raised.Body, "request", "id")
	decideBody := fmt.Sprintf(`{"approve":true,"expectedRevision":%d}`, f.number(raised.Body, "request", "revision"))
	decidePath := requestPath + "/" + id + "/decide"
	if r := f.do(http.MethodPost, decidePath, decideBody, agent); r.Status != http.StatusForbidden {
		t.Fatalf("agent approve: %d %s", r.Status, r.Raw)
	}
	approval := f.requestApproval(http.MethodPost, decidePath, decideBody)
	f.confirmApproval(approval)
	decided := f.submitWithApproval(approval)
	if decided.Status != http.StatusAccepted {
		t.Fatalf("decide: %d %s", decided.Status, decided.Raw)
	}
	request, found, err := f.store.GetWorkflowTaskRequest(context.Background(), wid, id)
	if err != nil || !found {
		t.Fatalf("read request: %v %v", found, err)
	}
	path := requestPath + "/" + id + "/materialize"
	body := fmt.Sprintf(`{"requestSha":%q,"expectedRequestRevision":%d,"expectedWorkflowRevision":%d,"idempotencyKey":"materialize-once","reason":"TEST_APPROVED_REQUEST"}`, request.RequestSHA, request.Revision, request.WorkflowRevision)
	if r := f.do(http.MethodPost, path, body, agent); r.Status != http.StatusForbidden {
		t.Fatalf("agent materialize: %d %s", r.Status, r.Raw)
	}
	if r := f.do(http.MethodPost, path, body, map[string]string{"X-AO-Operator-Approval": approval.ID}); r.Status != http.StatusForbidden {
		t.Fatalf("reuse decide approval: %d %s", r.Status, r.Raw)
	}
	before, err := f.store.ListWorkflowTasks(context.Background(), wid, "", 100, 0)
	if err != nil || len(before) != 1 {
		t.Fatalf("unapproved created tasks=%d err=%v", len(before), err)
	}
	created := f.asOperator(http.MethodPost, path, body)
	if created.Status != http.StatusCreated {
		t.Fatalf("materialize: %d %s", created.Status, created.Raw)
	}
	replay := f.asOperator(http.MethodPost, path, body)
	if replay.Status != http.StatusOK || replay.Body["created"] != false || f.text(replay.Body, "materialization", "taskId") != f.text(created.Body, "materialization", "taskId") {
		t.Fatalf("replay: %d %s", replay.Status, replay.Raw)
	}
	after, err := f.store.ListWorkflowTasks(context.Background(), wid, "", 100, 0)
	if err != nil || len(after) != 2 {
		t.Fatalf("materialized tasks=%d err=%v", len(after), err)
	}
	if len(f.sessionsFor()) != 1 {
		t.Fatal("materialization started an unadmitted session")
	}
}
