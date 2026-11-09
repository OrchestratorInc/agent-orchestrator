package controllers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// WorkflowReadAccessResponse 只给 Main，Renderer 只得到授权到期时间。
type WorkflowReadAccessResponse struct {
	ReadToken string    `json:"readToken"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// workflowReadAccess 用既有一次性 OS 审批授权有限期只读刷新，不能用于任何写操作。
func (c *OperatorController) workflowReadAccess(w http.ResponseWriter, r *http.Request) {
	if c.Principals == nil || c.Approvals == nil {
		envelope.WriteError(w, r, apierr.Forbidden("READ_ACCESS_UNAVAILABLE", "只读授权未装配"))
		return
	}
	bound, ok := withApprovalDigest(w, r)
	if !ok {
		return
	}
	r = bound
	caller, err := c.Principals.Resolve(r.Context(), principal.RequestFacts{Request: r})
	if err != nil {
		envelope.WriteError(w, r, principalError(err))
		return
	}
	if !caller.IsOperator() || caller.OperatorTokenFingerprint == "" {
		envelope.WriteError(w, r, apierr.Forbidden("READ_ACCESS_DENIED", "只读授权需要原生验证"))
		return
	}
	token, expires, err := c.Approvals.GrantWorkflowReadAccess(chi.URLParam(r, "workflowId"), caller.OperatorTokenFingerprint)
	if err != nil {
		envelope.WriteError(w, r, apierr.InternalCause("READ_ACCESS_FAILED", "无法建立只读授权", err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	envelope.WriteJSON(w, http.StatusOK, WorkflowReadAccessResponse{ReadToken: token, ExpiresAt: expires})
}
