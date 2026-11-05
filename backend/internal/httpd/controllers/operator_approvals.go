package controllers

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// OperatorController 仅允许本地进程申请审批；授权由 daemon 调用 OS 验证后逐操作发放。
type OperatorController struct {
	Approvals *principal.ApprovalStore
	Consent   principal.ConsentVerifier
}

// Register 挂载审批请求、状态与 OS 验证入口；不公开可枚举的审批列表。
func (c *OperatorController) Register(r chi.Router) {
	r.Post("/operator/approvals", c.createApproval)
	r.Post("/operator/approvals/{approvalId}/confirm", c.confirmApproval)
	r.Post("/operator/approvals/{approvalId}/status", c.approvalStatus)
}

// CreateOperatorApprovalRequest names the operation and payload a human is being asked about.
//
// Method and Path are the action's own method and concrete path. PayloadDigest is
// "sha256:<hex>" over the exact bytes the requester will send, computed by the requester with
// principal.PayloadDigest. Both Operation and PayloadDigest are shown to the human verbatim.
type CreateOperatorApprovalRequest struct {
	// OperationMethod 是本次审批的 HTTP 方法。
	OperationMethod string `json:"operationMethod" minLength:"3" maxLength:"8"`
	// OperationPath is the CONCRETE path of the action being authorised, for example
	// "/api/v1/workflows/w1/attempts/a1/bind". It must match an allowlisted operator route.
	OperationPath string `json:"operationPath" minLength:"1" maxLength:"1024"`
	// PayloadDigest is principal.PayloadDigest of the exact request body.
	PayloadDigest string `json:"payloadDigest" minLength:"1" maxLength:"128"`
	// Payload 是即将提交的精确 JSON 字符串，无正文时为空字符串。
	Payload string `json:"payload" maxLength:"65536"`
}

// OperatorApprovalOperationResponse is the operation a human is approving.
type OperatorApprovalOperationResponse struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// OperatorApprovalResponse is the create/confirm/status shape.
//
// It never carries the operator token, the confirming credential, or a fingerprint of it. The
// code appears ONLY in the create response, to the process that just minted it.
type OperatorApprovalResponse struct {
	ApprovalID string                            `json:"approvalId"`
	Code       string                            `json:"code,omitempty"`
	State      string                            `json:"state" enum:"pending,confirmed,consumed,expired"`
	Operation  OperatorApprovalOperationResponse `json:"operation"`
	// PayloadDigest is echoed so the terminal and the desktop UI can be compared by eye.
	PayloadDigest string     `json:"payloadDigest,omitempty"`
	ExpiresAt     time.Time  `json:"expiresAt"`
	ConfirmedAt   *time.Time `json:"confirmedAt,omitempty"`
}

// OperatorApprovalEnvelope wraps one approval.
type OperatorApprovalEnvelope struct {
	Approval OperatorApprovalResponse `json:"approval"`
}

// ConfirmOperatorApprovalRequest is the human gesture.
//
// Code and PayloadDigest must repeat exactly what the UI displayed, so a confirmation cannot be
// pointed at a payload nobody looked at.
type ConfirmOperatorApprovalRequest struct {
	Code          string `json:"code" minLength:"1" maxLength:"64"`
	PayloadDigest string `json:"payloadDigest" minLength:"1" maxLength:"128"`
}

// OperatorApprovalStatusRequest is the polling read: the id is in the path, the code in the
// body. Together they are a knowledge proof that grants nothing.
type OperatorApprovalStatusRequest struct {
	Code string `json:"code" minLength:"1" maxLength:"64"`
}

// JSON 字符串转义最多将正文放大六倍，仍单独限制解码后的 payload。
const maxApprovalBodyBytes = 6*principal.MaxApprovalPayloadBytes + (8 << 10)

// createApproval records a pending approval. Loopback only; grants nothing.
func (c *OperatorController) createApproval(w http.ResponseWriter, r *http.Request) {
	if c.Approvals == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/operator/approvals")
		return
	}
	// 审批只接受本地 listener 请求，不能经 LAN 触发本机人工验证。
	if !principal.LoopbackPeer(r) {
		envelope.WriteError(w, r, apierr.Forbidden(principal.CodeApprovalRequired,
			"Operator approvals are only accepted from this machine"))
		return
	}
	// An agent is refused here too, even though creating an approval confers nothing: a mint is
	// what produces the prompt a human is asked to answer, and an agent that could mint one could
	// put an operation in front of the human while wearing the desktop app's screen. Refusing
	// costs it nothing it can do legitimately — it cannot confirm the approval either.
	if hasOperatorAgentHeaders(r) {
		envelope.WriteError(w, r, apierr.Forbidden(principal.CodeApprovalRequired,
			"Operator approvals are requested by a local non-agent process"))
		return
	}
	var req CreateOperatorApprovalRequest
	if err := decodeApprovalBody(w, r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON",
			"Invalid JSON body", nil)
		return
	}
	approval, err := c.Approvals.CreateWithPayload(req.OperationMethod, req.OperationPath, req.PayloadDigest, req.Payload)
	if err != nil {
		envelope.WriteError(w, r, operatorApprovalError2(err))
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, OperatorApprovalEnvelope{
		Approval: operatorApprovalResponse(
			approval, string(approval.State(c.Approvals.Now())), true),
	})
}

// confirmApproval 只信任 OS 验证结果；token、请求正文和 Agent 凭据都不能代替人工验证。
func (c *OperatorController) confirmApproval(w http.ResponseWriter, r *http.Request) {
	if c.Approvals == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/operator/approvals/{approvalId}/confirm")
		return
	}
	if !principal.LoopbackPeer(r) || hasOperatorAgentHeaders(r) {
		envelope.WriteError(w, r, apierr.Forbidden(principal.CodeApprovalRequired,
			"Native operator verification requires a local non-agent request"))
		return
	}
	var req ConfirmOperatorApprovalRequest
	if err := decodeApprovalBody(w, r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	approval, err := c.Approvals.VerifyAndConfirm(r.Context(),
		chi.URLParam(r, "approvalId"), req.Code, req.PayloadDigest, c.Consent)
	if err != nil {
		envelope.WriteError(w, r, operatorApprovalError2(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, OperatorApprovalEnvelope{
		Approval: operatorApprovalResponse(approval, string(principal.ApprovalStateConfirmed), false),
	})
}

// hasOperatorAgentHeaders 对已声明 Agent 身份的请求提前拒绝；删除标记也仍须经过 OS 验证。
func hasOperatorAgentHeaders(r *http.Request) bool {
	for _, header := range []string{principal.HeaderAgentCapability, principal.HeaderAttemptLease,
		"X-AO-Session-Id", principal.HeaderAttemptID, "X-AO-Workflow-Id"} {
		if strings.TrimSpace(r.Header.Get(header)) != "" {
			return true
		}
	}
	return false
}

// approvalStatus lets the requester poll. Id + code required; state only.
func (c *OperatorController) approvalStatus(w http.ResponseWriter, r *http.Request) {
	if c.Approvals == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/operator/approvals/{approvalId}/status")
		return
	}
	if !principal.LoopbackPeer(r) || hasOperatorAgentHeaders(r) {
		envelope.WriteError(w, r, apierr.Forbidden(principal.CodeApprovalRequired,
			"Operator approvals are only readable from this machine by a non-agent process"))
		return
	}
	var req OperatorApprovalStatusRequest
	if err := decodeApprovalBody(w, r, &req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON",
			"Invalid JSON body", nil)
		return
	}
	approval, found := c.Approvals.Get(chi.URLParam(r, "approvalId"))
	if !found {
		envelope.WriteError(w, r, operatorApprovalError(&principal.ApprovalRefusal{
			Code: principal.CodeApprovalInvalid, Reason: principal.ReasonApprovalUnknown,
			Message: "No such approval",
		}))
		return
	}
	// The code is a knowledge proof for this read: it is 32 random bits minted with the id, and
	// comparing it keeps one local process from polling another's pending approvals.
	if !principal.ApprovalCodeMatches(approval, req.Code) {
		envelope.WriteError(w, r, operatorApprovalError(&principal.ApprovalRefusal{
			Code: principal.CodeApprovalInvalid, Reason: principal.ReasonApprovalCodeMismatch,
			Message: "The approval code does not match",
		}))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, OperatorApprovalEnvelope{
		Approval: operatorApprovalResponse(approval, string(approval.State(c.Approvals.Now())), false),
	})
}

// decodeApprovalBody bounds and strictly decodes one approval body.
func decodeApprovalBody(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxApprovalBodyBytes)
	return decodeJSONStrict(r, out)
}

// operatorApprovalResponse is the wire shape of one approval.
func operatorApprovalResponse(
	approval principal.Approval,
	state string,
	includeCode bool,
) OperatorApprovalResponse {
	out := OperatorApprovalResponse{
		ApprovalID: approval.ID,
		State:      state,
		Operation: OperatorApprovalOperationResponse{
			Method: approval.Method,
			Path:   approval.Path,
		},
		ExpiresAt:   approval.ExpiresAt.UTC(),
		ConfirmedAt: approval.ConfirmedAt,
	}
	// 普通响应只返回元数据；完整正文仅传给 OS 验证器展示。
	out.PayloadDigest = approval.PayloadDigest
	// 配对码只在创建时返回；单条状态与确认响应均不再回显。
	if includeCode {
		out.Code = approval.Code
	}
	return out
}

// operatorApprovalError2 renders any approval error onto the wire: a typed refusal keeps its
// stable code, anything else becomes a 400 (a create failure names an operation no approval can
// authorise, which is a request problem, not a server fault).
func operatorApprovalError2(err error) error {
	if refusal, ok := principal.AsApprovalRefusal(err); ok {
		return operatorApprovalError(refusal)
	}
	return apierr.Invalid("OPERATOR_OPERATION_NOT_APPROVABLE",
		"The requested approval could not be created", nil)
}

// operatorApprovalError maps a typed approval refusal onto a 403 with its stable code.
//
// The reason travels in details.reason: it describes a STATE ("expired", "consumed",
// "operation mismatch"), never a secret, and a client needs it to decide whether to request a new
// approval or fix the request.
func operatorApprovalError(refusal *principal.ApprovalRefusal) error {
	code := refusal.Code
	if strings.TrimSpace(code) == "" {
		code = principal.CodeApprovalInvalid
	}
	message := refusal.Message
	if strings.TrimSpace(message) == "" {
		message = "The operator approval cannot authorise this request"
	}
	details := map[string]any{}
	if refusal.Reason != "" {
		details["reason"] = refusal.Reason
	}
	return apierr.New(apierr.KindForbidden, code, message, details)
}
