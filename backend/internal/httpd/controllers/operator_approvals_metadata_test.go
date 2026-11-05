package controllers_test

import (
	"encoding/json"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"net/http"
	"strings"
	"testing"
)

// TestM2OperatorApprovalResponsesCarryNoToken sweeps every route this change adds or touches.
func TestM2OperatorApprovalResponsesCarryNoToken(t *testing.T) {
	t.Parallel()
	env := newApprovalEnv(t)
	bindBody := `{"idempotencyKey":"token-sweep"}`
	digest := principal.PayloadDigest([]byte(bindBody))

	var bodies []string
	collect := func(status int, raw []byte) {
		t.Helper()
		if status == 0 {
			return
		}
		bodies = append(bodies, string(raw))
	}

	createRequest, _ := json.Marshal(map[string]string{
		"operationMethod": "POST", "operationPath": env.bindPath(), "payloadDigest": digest, "payload": bindBody,
	})
	status, raw := doJSONWithHeaders(t, env.srv, http.MethodPost, "/api/v1/operator/approvals", string(createRequest), nil)
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, raw)
	}
	collect(status, raw)
	var created approvalDTO
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	status, raw = doJSONWithHeaders(t, env.srv, http.MethodPost,
		"/api/v1/operator/approvals/"+created.Approval.ApprovalID+"/status",
		`{"code":"`+created.Approval.Code+`"}`, nil)
	collect(status, raw)

	status, raw = doJSONWithHeaders(t, env.srv, http.MethodPost,
		"/api/v1/operator/approvals/"+created.Approval.ApprovalID+"/confirm",
		`{"code":"`+created.Approval.Code+`","payloadDigest":"`+digest+`"}`,
		map[string]string{principal.HeaderOperatorToken: env.token})
	collect(status, raw)

	status, raw = env.submitAsApproval(t, http.MethodPost, env.bindPath(), bindBody, created.Approval.ApprovalID)
	collect(status, raw)

	status, raw = doJSONWithHeaders(t, env.srv, http.MethodPost, env.bindPath(), bindBody, nil)
	collect(status, raw)

	for _, body := range bodies {
		if strings.Contains(body, env.token) {
			t.Fatalf("an operator response body carries the operator token: %s", body)
		}
		if strings.Contains(body, principal.OperatorTokenFingerprint(env.token)) {
			t.Fatalf("an operator response body carries the token fingerprint: %s", body)
		}
	}
}

// TestM2OperatorApprovalRestartInvalidatesApprovalsAndRefusesToken 验证重启废止审批，旧 token 也不能授权。
func TestM2OperatorApprovalRestartInvalidatesApprovalsAndRefusesToken(t *testing.T) {
	t.Parallel()
	env := newApprovalEnv(t)
	bindBody := `{"idempotencyKey":"restart-bind"}`
	digest := principal.PayloadDigest([]byte(bindBody))
	approval := env.createApproval(t, http.MethodPost, env.bindPath(), bindBody)
	env.confirmApproval(t, approval, digest)

	// 重启重新创建审批表；不从磁盘读取或恢复任何 Operator 授权。
	env.approvals.ResetApprovals()

	status, raw := env.submitAsApproval(t, http.MethodPost, env.bindPath(), bindBody, approval.Approval.ApprovalID)
	if status != http.StatusForbidden || !strings.Contains(string(raw), principal.CodeApprovalInvalid) {
		t.Fatalf("post-restart approval = %d; body=%s", status, raw)
	}
	// 重启后旧 token 同样不能变成 Operator 身份。
	status, raw = doJSONWithHeaders(t, env.srv, http.MethodPost, env.bindPath(), bindBody,
		map[string]string{principal.HeaderOperatorToken: env.token})
	if status != http.StatusForbidden {
		t.Fatalf("token path after restart = %d; body=%s", status, raw)
	}
}
