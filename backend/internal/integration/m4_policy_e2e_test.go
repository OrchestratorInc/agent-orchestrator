package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

// m4CheckPolicyReadback 复用修复链的工作流；删除当前策略投影、目录 revision 或配置 CAS 会使断言失败。
func m4CheckPolicyReadback(t *testing.T, f *m2ffFixture, w domain.WorkflowID) {
	t.Helper()
	base := "/workflows/" + string(w)
	p := m4OfflinePolicy(t)
	readback := func(want *controllers.QualityPolicyResponse) *controllers.QualityPolicyResponse {
		t.Helper()
		summary := m4HTTP[map[string]json.RawMessage](t, f, http.MethodGet, base+"/quality-summary", nil)
		raw, exists := summary["currentPolicy"]
		if !exists {
			t.Fatal("质量摘要缺少 currentPolicy")
		}
		var current *controllers.QualityPolicyResponse
		if err := json.Unmarshal(raw, &current); err != nil {
			t.Fatal(err)
		}
		var revision int64
		if want == nil {
			if current != nil {
				t.Fatalf("未配置策略应返回 null: %+v", current)
			}
		} else {
			if current == nil || *current != *want {
				t.Fatalf("策略读回=%+v，期望=%+v", current, want)
			}
			revision = current.Revision
		}
		policies := m4HTTP[controllers.QualityPoliciesResponse](t, f, http.MethodGet, base+"/quality-policies", nil)
		found := false
		for _, candidate := range policies.Policies {
			if candidate.Revision != revision {
				t.Fatalf("策略目录 CAS 版本=%d，当前记录=%d", candidate.Revision, revision)
			}
			if candidate.ID == p.ID {
				found = true
				if candidate.PolicyRevision != p.Revision || candidate.Digest != p.Digest() {
					t.Fatalf("可信策略身份不匹配: %+v", candidate)
				}
			}
		}
		if !found {
			t.Fatal("策略目录缺少已装配的离线策略")
		}
		return current
	}

	readback(nil)
	configured := m4HTTP[controllers.QualityPolicyResponse](t, f, http.MethodPut, base+"/quality-policy", controllers.QualityPolicyRequest{PolicyID: p.ID})
	if configured.ID != p.ID || configured.PolicyRevision != p.Revision || configured.Digest != p.Digest() || configured.Revision <= 0 {
		t.Fatalf("配置策略身份或记录版本无效: %+v", configured)
	}
	current := readback(&configured)
	req := controllers.QualityPolicyRequest{PolicyID: current.ID, ExpectedRevision: current.Revision}
	updated := m4HTTP[controllers.QualityPolicyResponse](t, f, http.MethodPut, base+"/quality-policy", req)
	want := *current
	want.Revision++
	if updated != want {
		t.Fatalf("使用读回版本更新=%+v，期望=%+v", updated, want)
	}
	readback(&updated)
	stale := f.asOperator(http.MethodPut, base+"/quality-policy", m3JSON(t, req))
	if stale.Status != http.StatusConflict || f.text(stale.Body, "code") != "ARTIFACT_STALE" {
		t.Fatalf("旧策略版本未被 CAS 拒绝: %d %s", stale.Status, stale.Raw)
	}
	readback(&updated)
}
