package controllers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	integrationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/integration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// preview 不授予目标分支写权，只生成完整持久预演。
func (c *WorkflowQualityController) preview(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.identity(w, r); !ok {
		return
	}
	var req QualityPreviewRequest
	if !qualityDecode(w, r, &req) {
		return
	}
	f, ok := c.fence(w, r)
	if !ok {
		return
	}
	p, entries, err := c.Integrations.Preview(r.Context(), req.IdempotencyKey, domain.WorkflowID(chi.URLParam(r, "workflowId")), req.ArtifactIDs, f)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, IntegrationPlanResponse{Plan: p, Repositories: entries})
}

// plan 检查所属 Workflow 后读取冻结证据，不能读到其他工作流的计划。
func (c *WorkflowQualityController) plan(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.identity(w, r); !ok {
		return
	}
	p, found, err := c.Integrations.Store.GetIntegrationPlan(r.Context(), chi.URLParam(r, "planId"))
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	if !found || p.WorkflowID != domain.WorkflowID(chi.URLParam(r, "workflowId")) {
		envelope.WriteError(w, r, apierr.NotFound("INTEGRATION_NOT_FOUND", "No integration in this workflow"))
		return
	}
	entries, err := c.Integrations.Store.ListIntegrationRepoPreviews(r.Context(), p.ID)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, IntegrationPlanResponse{Plan: p, Repositories: entries})
}

func (c *WorkflowQualityController) apply(w http.ResponseWriter, r *http.Request) {
	c.integrationWrite(w, r, "apply")
}
func (c *WorkflowQualityController) push(w http.ResponseWriter, r *http.Request) {
	c.integrationWrite(w, r, "push")
}

// integrationWrite 保留原始正文用于服务内单次消费；Agent 或旧 lease 标头存在即拒绝，不能降级人工身份。
// 已消费批准只可读取完全相同意图的结果，绝不重新进入 Git 写路径。
func (c *WorkflowQualityController) integrationWrite(w http.ResponseWriter, r *http.Request, action string) {
	if !c.ready(w, r) {
		return
	}
	if c.Integrations.Approvals == nil || !principal.LoopbackPeer(r) {
		envelope.WriteError(w, r, apierr.Forbidden("OPERATOR_APPROVAL_REQUIRED", "A local native approval is required"))
		return
	}
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	planID := chi.URLParam(r, "planId")
	if r.URL.RequestURI() != integrationsvc.Path(workflowID, planID, action) {
		envelope.WriteError(w, r, apierr.Forbidden("OPERATOR_APPROVAL_INVALID", "The exact approved request URI is required"))
		return
	}
	for key := range r.Header {
		for _, agentHeader := range []string{principal.HeaderAgentCapability, principal.HeaderSessionID, principal.HeaderAttemptID, principal.HeaderAttemptLease, "X-AO-Workflow-Lease"} {
			if strings.EqualFold(key, agentHeader) {
				envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED", "This operation requires only human approval"))
				return
			}
		}
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, principal.MaxApprovalPayloadBytes))
	if err != nil {
		envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Invalid integration body", nil))
		return
	}
	id := strings.TrimSpace(r.Header.Get(principal.HeaderOperatorApproval))
	a, found := c.Integrations.Approvals.Get(id)
	digest := principal.PayloadDigest(raw)
	if !found || !a.Confirmed || a.ConfirmedByFingerprint == "" || !a.ExpiresAt.After(c.Workflows.now()) || a.Method != http.MethodPost || a.Path != r.URL.RequestURI() || a.PayloadDigest != digest {
		envelope.WriteError(w, r, apierr.Forbidden("OPERATOR_APPROVAL_REQUIRED", "A current exact native approval is required"))
		return
	}
	if a.ConsumedAt != nil {
		var req domain.IntegrationApplyRequest
		if json.Unmarshal(raw, &req) != nil {
			envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Invalid integration body", nil))
			return
		}
		op, found, err := c.Store.GetIntegrationOperation(r.Context(), req.IdempotencyKey)
		if err != nil {
			envelope.WriteError(w, r, qualityError(err))
			return
		}
		if !found || op.WorkflowID != workflowID || op.PlanID != planID || op.Action != action || op.RequestDigest != digest {
			envelope.WriteError(w, r, apierr.Forbidden("OPERATOR_APPROVAL_INVALID", "Consumed approval has no matching persisted intent"))
			return
		}
		c.operationResult(w, r, op)
		return
	}
	f, ok := c.fence(w, r)
	if !ok {
		return
	}
	var op domain.IntegrationOperation
	if action == "apply" {
		op, err = c.Integrations.Apply(r.Context(), workflowID, planID, raw, id, f)
	} else {
		op, err = c.Integrations.Push(r.Context(), workflowID, planID, raw, id, f)
	}
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	c.operationResult(w, r, op)
}

// operation 通过原 principal 检查后展示逐仓事实，不暴露批准引用或内部请求正文。
func (c *WorkflowQualityController) operation(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.identity(w, r); !ok {
		return
	}
	op, ok := c.operationTarget(w, r)
	if !ok {
		return
	}
	c.operationResult(w, r, op)
}

// operationTarget 以 URL 所属 Workflow 限制所有操作读取和恢复。
func (c *WorkflowQualityController) operationTarget(w http.ResponseWriter, r *http.Request) (domain.IntegrationOperation, bool) {
	op, found, err := c.Store.GetIntegrationOperation(r.Context(), chi.URLParam(r, "operationId"))
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return op, false
	}
	if !found || op.WorkflowID != domain.WorkflowID(chi.URLParam(r, "workflowId")) {
		envelope.WriteError(w, r, apierr.NotFound("INTEGRATION_NOT_FOUND", "No operation in this workflow"))
		return op, false
	}
	return op, true
}

// operationResult 保留每仓结果，不能把 partial/unknown 显示成全局成功。
func (c *WorkflowQualityController) operationResult(w http.ResponseWriter, r *http.Request, op domain.IntegrationOperation) {
	entries, err := c.Store.ListIntegrationRepoOperations(r.Context(), op.ID)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, IntegrationOperationResponse{Operation: op, Repositories: entries})
}

// reconcile 需要当前具体操作的人工批准，只读 Git 并记录事实；不复用原写权限。
func (c *WorkflowQualityController) reconcile(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	var req QualityReconcileRequest
	if _, ok := c.Workflows.operatorWrite(w, r, &req); !ok {
		return
	}
	op, ok := c.operationTarget(w, r)
	if !ok {
		return
	}
	if op.Revision != req.ExpectedRevision {
		envelope.WriteError(w, r, qualityError(domain.ErrArtifactStale))
		return
	}
	f, ok := c.fence(w, r)
	if !ok {
		return
	}
	result, err := c.Integrations.Reconcile(r.Context(), op.WorkflowID, op.ID, req.ExpectedRevision, f)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	c.operationResult(w, r, result)
}
