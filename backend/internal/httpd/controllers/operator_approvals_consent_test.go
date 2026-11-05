package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// consentFunc 代替外部 OS 服务；正式生产装配始终使用原生验证器。
type consentFunc func(context.Context, principal.Approval) error

func (f consentFunc) Verify(ctx context.Context, approval principal.Approval) error {
	return f(ctx, approval)
}

// TestM2OperatorConfirmationRejectsForgedConsent 请求不能携带自称通过 OS 的字段或借 Agent 身份触发验证。
func TestM2OperatorConfirmationRejectsForgedConsent(t *testing.T) {
	env := newApprovalEnv(t)
	called := false
	env.deps.OperatorConsent = consentFunc(func(context.Context, principal.Approval) error { called = true; return nil })
	env.srv = env.serverWith(t, env.deps.WorkflowPrincipals)
	body := `{"idempotencyKey":"forged-consent"}`
	approval := env.createApproval(t, http.MethodPost, env.bindPath(), body)
	confirmPath := "/api/v1/operator/approvals/" + approval.Approval.ApprovalID + "/confirm"
	request, _ := json.Marshal(map[string]any{"code": approval.Approval.Code,
		"payloadDigest": approval.Approval.PayloadDigest, "verified": true, "identity": "windows-hello"})
	status, raw := doJSONWithHeaders(t, env.srv, http.MethodPost, confirmPath, string(request), nil)
	if status != http.StatusBadRequest {
		t.Fatalf("forged OS claims=%d: %s", status, raw)
	}
	request, _ = json.Marshal(map[string]string{"code": approval.Approval.Code, "payloadDigest": approval.Approval.PayloadDigest})
	status, raw = doJSONWithHeaders(t, env.srv, http.MethodPost, confirmPath, string(request),
		map[string]string{principal.HeaderAgentCapability: "agent-capability", principal.HeaderAttemptLease: "lease"})
	if status != http.StatusForbidden || called {
		t.Fatalf("agent triggered native verification: %d %s called=%v", status, raw, called)
	}
	status, raw = env.submitAsApproval(t, http.MethodPost, env.bindPath(), body, approval.Approval.ApprovalID)
	if status != http.StatusForbidden || env.gateway.SpawnCalls() != 0 {
		t.Fatalf("unverified action=%d: %s", status, raw)
	}
}

// TestM2OperatorPayloadBoundaryAndMetadata 强制大小、有效正文与digest一致，普通元数据接口不暴露正文。
func TestM2OperatorPayloadBoundaryAndMetadata(t *testing.T) {
	env := newApprovalEnv(t)
	for _, payload := range []string{`{"changed":true}`, `"` + strings.Repeat("a", principal.MaxApprovalPayloadBytes) + `"`} {
		request, _ := json.Marshal(map[string]string{"operationMethod": "POST", "operationPath": env.bindPath(),
			"payloadDigest": principal.PayloadDigest(nil), "payload": payload})
		status, raw := doJSONWithHeaders(t, env.srv, http.MethodPost, "/api/v1/operator/approvals", string(request), nil)
		if status != http.StatusBadRequest && status != http.StatusForbidden {
			t.Fatalf("invalid payload accepted=%d: %s", status, raw)
		}
	}
	body := `{"idempotencyKey":"private-payload-marker"}`
	approval := env.createApproval(t, http.MethodPost, env.bindPath(), body)
	statusBody, _ := json.Marshal(map[string]string{"code": approval.Approval.Code})
	status, raw := doJSONWithHeaders(t, env.srv, http.MethodPost,
		"/api/v1/operator/approvals/"+approval.Approval.ApprovalID+"/status", string(statusBody), nil)
	if status != http.StatusOK || strings.Contains(string(raw), "private-payload-marker") {
		t.Fatalf("status disclosure=%d: %s", status, raw)
	}
	// The token-adoption surface is gone entirely: there is no credential to adopt, and an
	// unregistered route must answer 404 rather than a hint that one exists elsewhere.
	status, raw = doJSONWithHeaders(t, env.srv, http.MethodPost, "/api/v1/operator/token/adopt", "{}",
		map[string]string{principal.HeaderOperatorToken: env.token})
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		t.Fatalf("obsolete credential adoption remains=%d: %s", status, raw)
	}
}

// TestM2OperatorReadApprovalBindsQuery 通过真实 router 检验解码前的绑定，替换分页不能借用原审批。
func TestM2OperatorReadApprovalBindsQuery(t *testing.T) {
	env := newApprovalEnv(t)
	path := "/api/v1/workflows/" + env.workflowID + "/messages?limit=1&offset=0"
	approval := env.createApproval(t, http.MethodGet, path, "")
	env.confirmApproval(t, approval, principal.PayloadDigest(nil))
	status, raw := env.submitAsApproval(t, http.MethodGet, strings.Replace(path, "limit=1", "limit=2", 1), "", approval.Approval.ApprovalID)
	if status != http.StatusForbidden || !strings.Contains(string(raw), principal.ReasonApprovalOperationMismatch) {
		t.Fatalf("query substitution=%d: %s", status, raw)
	}
	status, raw = env.submitAsApproval(t, http.MethodGet, path, "", approval.Approval.ApprovalID)
	if status != http.StatusOK {
		t.Fatalf("approved query=%d: %s", status, raw)
	}
}

// TestM2OperatorTokenCannotAuthorizeOrSelfConfirm 覆盖同用户进程读到 token 后的两条提权路径。
func TestM2OperatorTokenCannotAuthorizeOrSelfConfirm(t *testing.T) {
	env := newApprovalEnv(t)
	env.deps.OperatorConsent = nil
	env.srv = env.serverWith(t, env.deps.WorkflowPrincipals)
	path := "/api/v1/workflows/" + env.workflowID + "/attempts/" + env.attemptID + "/bind"
	body := `{"idempotencyKey":"stolen-token"}`
	headers := map[string]string{principal.HeaderOperatorToken: env.token}
	t.Run("token cannot execute", func(t *testing.T) {
		status, raw := doJSONWithHeaders(t, env.srv, http.MethodPost, path, body, headers)
		if status != http.StatusForbidden {
			t.Fatalf("token executed an operator action: status=%d body=%s", status, raw)
		}
	})
	t.Run("token cannot confirm without OS consent", func(t *testing.T) {
		approval := env.createApproval(t, http.MethodPost, path, body)
		request, _ := json.Marshal(map[string]string{"code": approval.Approval.Code, "payloadDigest": approval.Approval.PayloadDigest})
		status, raw := doJSONWithHeaders(t, env.srv, http.MethodPost,
			"/api/v1/operator/approvals/"+approval.Approval.ApprovalID+"/confirm", string(request), headers)
		if status != http.StatusForbidden {
			t.Fatalf("token self-confirmed without native consent: status=%d body=%s", status, raw)
		}
	})
}

// TestM2OperatorApprovalCannotBeEnumeratedAndStolen 覆盖去除 Agent headers 后列出并兑付他人空正文审批的攻击。
func TestM2OperatorApprovalCannotBeEnumeratedAndStolen(t *testing.T) {
	env := newApprovalEnv(t)
	path := "/api/v1/workflows/" + env.workflowID + "/messages"
	approval := env.createApproval(t, http.MethodGet, path, "")
	env.confirmApproval(t, approval, principal.PayloadDigest(nil))
	status, raw := doJSONWithHeaders(t, env.srv, http.MethodGet, "/api/v1/operator/approvals", "", nil)
	var discovered struct {
		Approvals []struct {
			ApprovalID string `json:"approvalId"`
		} `json:"approvals"`
	}
	_ = json.Unmarshal(raw, &discovered)
	for _, row := range discovered.Approvals {
		if row.ApprovalID != "" {
			stolenStatus, stolenBody := env.submitAsApproval(t, http.MethodGet, path, "", row.ApprovalID)
			t.Fatalf("enumeration exposed a redeemable approval: redemption=%d body=%s",
				stolenStatus, stolenBody)
		}
	}
	if status != http.StatusMethodNotAllowed || strings.Contains(string(raw), approval.Approval.ApprovalID) ||
		strings.Contains(string(raw), approval.Approval.Code) {
		t.Fatalf("approval enumeration remained available: %d %s", status, raw)
	}
	statusPath := "/api/v1/operator/approvals/" + approval.Approval.ApprovalID + "/status"
	status, raw = doJSONWithHeaders(t, env.srv, http.MethodPost, statusPath, `{"code":"wrong-code"}`, nil)
	if status != http.StatusForbidden || strings.Contains(string(raw), approval.Approval.Code) {
		t.Fatalf("status exposed pairing code: %d %s", status, raw)
	}
	request, _ := json.Marshal(map[string]string{"code": approval.Approval.Code})
	status, raw = doJSONWithHeaders(t, env.srv, http.MethodPost, statusPath, string(request), nil)
	if status != http.StatusOK || strings.Contains(string(raw), approval.Approval.Code) {
		t.Fatalf("owner status echoed pairing code: %d %s", status, raw)
	}
	status, raw = env.submitAsApproval(t, http.MethodGet, path, "", approval.Approval.ApprovalID)
	if status != http.StatusOK {
		t.Fatalf("owner's approval was changed by refused enumeration: %d %s", status, raw)
	}
}
