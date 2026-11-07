package principal_test

import (
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// 删除范围、方法、过期或每次启动的校验会使拒绝用例变红；重复读取不得再次消费原生审批。
func TestWorkflowReadAccessScopedRefresh(t *testing.T) {
	f := newApprovalFixture(t)
	path := "/api/v1/workflows/w1/read-access"
	digest := principal.PayloadDigest(nil)
	a, err := f.store.CreateWithPayload(http.MethodPost, path, digest, "")
	if err != nil {
		t.Fatal(err)
	}
	approveAndConfirm(t, f, a.ID, a.Code, digest)
	resolver := principal.NewResolver(nil, nil, f.trust)
	caller, err := resolver.FromRequest(approvalRequest(a.ID, http.MethodPost, path, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := f.store.GrantWorkflowReadAccess("w1", caller.OperatorTokenFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/api/v1/workflows/w1/coordination-summary", "/api/v1/workflows/w1/messages", "/api/v1/workflows/w1/task-requests", "/api/v1/workflows/capabilities"} {
		for range 2 {
			r := approvalRequest("", http.MethodGet, route, "")
			r.Header.Set(principal.HeaderWorkflowReadAccess, token)
			got, err := resolver.FromRequest(r, "")
			if err != nil || !got.IsOperator() || got.LeaseProven {
				t.Fatalf("read %s: caller=%+v err=%v", route, got, err)
			}
		}
	}
	for _, route := range []string{"/api/v1/workflows/w2/messages", "/api/v1/workflows/w1/activate", "/api/v1/scheduler/policy", "/api/v1/workflows/w1/../w2/messages"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
			r := approvalRequest("", method, route, "")
			r.Header.Set(principal.HeaderWorkflowReadAccess, token)
			if _, err := resolver.FromRequest(r, ""); err == nil {
				t.Fatalf("grant authorized %s %s", method, route)
			}
		}
	}
	r := approvalRequest("", http.MethodGet, "/api/v1/workflows/w1/messages", "")
	r.Header.Set(principal.HeaderWorkflowReadAccess, token)
	r.RemoteAddr = "192.0.2.1:1234"
	if _, err := resolver.FromRequest(r, ""); err == nil {
		t.Fatal("LAN accepted")
	}
	r.RemoteAddr = "127.0.0.1:1234"
	restarted := principal.NewResolver(nil, nil, principal.ApprovalOperatorTrust{Approvals: principal.NewApprovalStore(principal.ApprovalStoreOptions{})})
	if _, err := restarted.FromRequest(r, ""); err == nil {
		t.Fatal("grant survived daemon restart")
	}
	f.tick(principal.WorkflowReadAccessTTL)
	if _, err := resolver.FromRequest(r, ""); err == nil {
		t.Fatal("expired grant accepted")
	}
}
