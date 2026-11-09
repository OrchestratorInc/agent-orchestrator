package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// m5VerifyReadAccess 经过真实 HTTP 审批和只读授权，验证自动刷新、能力身份和跨 Workflow 隔离。
// 删除 Session 探测、只读 Principal 或绑定归属检查，会使对应的行为断言失败。
func m5VerifyReadAccess(t *testing.T, f *m2ffFixture, a domain.WorkflowAttempt) {
	t.Helper()
	path := "/workflows/" + string(a.WorkflowID)
	capPath := f.attemptPath(a.WorkflowID, a.ID, "capabilities")
	if r := f.do(http.MethodGet, capPath, "", nil); r.Status != http.StatusForbidden {
		t.Fatalf("匿名读取能力: %d %s", r.Status, r.Raw)
	}
	grant := m4HTTP[controllers.WorkflowReadAccessResponse](t, f, http.MethodPost, path+"/read-access", nil)
	headers := map[string]string{principal.HeaderWorkflowReadAccess: grant.ReadToken}
	for range 2 {
		for _, route := range []string{path + "/coordination-summary", path + "/messages", path + "/task-requests", capPath, f.attemptPath(a.WorkflowID, a.ID, "interventions")} {
			r := f.do(http.MethodGet, route, "", headers)
			if r.Status != http.StatusOK {
				t.Fatalf("只读刷新 %s: %d %s", route, r.Status, r.Raw)
			}
			if route == capPath {
				var caps controllers.GetWorkflowCapabilitiesResponse
				if err := json.Unmarshal([]byte(r.Raw), &caps); err != nil {
					t.Fatal(err)
				}
				binding := m4HTTP[controllers.WorkflowBindingEnvelope](t, f, http.MethodGet, f.attemptPath(a.WorkflowID, a.ID, "binding"), nil).Binding
				if len(caps.Capabilities) != 1 {
					t.Fatalf("能力不是当前 Attempt: %+v", caps)
				}
				observed := caps.Capabilities[0]
				if observed.ObservedSessionID != string(*a.SessionID) || observed.Generation != binding.ControllerGeneration || observed.Queue != "supported" || observed.Interrupt != "supported" || observed.Expired {
					t.Fatalf("能力未来自当前 Controller: %+v", observed)
				}
			}
		}
	}
	otherPath := f.attemptPath("other-workflow", a.ID, "capabilities")
	for _, request := range []struct{ method, path string }{{http.MethodGet, otherPath}, {http.MethodPost, path + "/messages"}, {http.MethodPost, path + "/pause"}} {
		body := ""
		if request.method == http.MethodPost {
			body = "{}"
		}
		if r := f.do(request.method, request.path, body, headers); r.Status != http.StatusForbidden {
			t.Fatalf("只读授权越界 %s %s: %d %s", request.method, request.path, r.Status, r.Raw)
		}
	}
	if r := f.asOperator(http.MethodGet, otherPath, ""); r.Status != http.StatusNotFound {
		t.Fatalf("跨 Workflow 读取绑定能力: %d %s", r.Status, r.Raw)
	}
}
