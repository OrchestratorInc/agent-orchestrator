package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/artifact"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/coordination"
	integrationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/integration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualitygate"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualityreview"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/taskcommit"
	"github.com/go-chi/chi/v5"
)

// QualityReader 仅提供既有质量事实；写动作走各自服务及其事务栅栏。
type QualityReader interface {
	GetWorkflow(context.Context, domain.WorkflowID) (domain.Workflow, bool, error)
	GetWorkflowQualityPolicy(context.Context, domain.WorkflowID) (domain.WorkflowQualityPolicy, bool, error)
	GetArtifact(context.Context, string) (domain.ArtifactSnapshot, bool, error)
	GetQualityReview(context.Context, string) (domain.QualityReview, bool, error)
	ListArtifacts(context.Context, domain.WorkflowID) ([]domain.ArtifactSnapshot, error)
	ListGateRuns(context.Context, domain.WorkflowID) ([]domain.GateRun, error)
	ListQualityReviews(context.Context, domain.WorkflowID) ([]domain.QualityReview, error)
	ListTaskCommits(context.Context, domain.WorkflowID) ([]domain.TaskCommit, error)
	ListIntegrationPlans(context.Context, domain.WorkflowID) ([]domain.IntegrationPlan, error)
	ListIntegrationOperations(context.Context, domain.WorkflowID) ([]domain.IntegrationOperation, error)
	GetIntegrationOperation(context.Context, string) (domain.IntegrationOperation, bool, error)
	ListIntegrationRepoOperations(context.Context, string) ([]domain.IntegrationRepoOperation, error)
}

// WorkflowQualityController 在原 Workflow principal 边界上接线；不派生新的执行任期或批准主体。
type WorkflowQualityController struct {
	Workflows    *WorkflowsController
	Scheduler    *SchedulerController
	Store        QualityReader
	Artifacts    *artifact.Service
	Gates        *qualitygate.Service
	Reviews      *qualityreview.Service
	Commits      *taskcommit.Service
	Integrations *integrationsvc.Service
	Coordination *coordination.Service
}

// Register 的路由始终存在；缺少可信装配返回 501，不能降级绕开授权。
func (c *WorkflowQualityController) Register(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(operatorApprovalBinding)
		r.Get("/workflows/{workflowId}/quality-summary", c.summary)
		r.Get("/workflows/{workflowId}/coordination-summary", c.coordination)
		r.Get("/workflows/{workflowId}/quality-policies", c.policies)
		r.Put("/workflows/{workflowId}/quality-policy", c.configure)
		r.Post("/workflows/{workflowId}/attempts/{attemptId}/artifacts/snapshot", c.snapshot)
		r.Post("/workflows/{workflowId}/artifacts/{artifactId}/gates", c.gate)
		r.Post("/workflows/{workflowId}/artifacts/{artifactId}/reviews", c.review)
		r.Post("/workflows/{workflowId}/reviews/{reviewId}/finish", c.finishReview)
		r.Post("/workflows/{workflowId}/artifacts/{artifactId}/commit", c.commit)
		r.Post("/workflows/{workflowId}/integrations/preview", c.preview)
		r.Get("/workflows/{workflowId}/integrations/{planId}", c.plan)
		r.Post("/workflows/{workflowId}/integrations/{planId}/apply", c.apply)
		r.Post("/workflows/{workflowId}/integrations/{planId}/push", c.push)
		r.Get("/workflows/{workflowId}/integration-operations/{operationId}", c.operation)
		r.Post("/workflows/{workflowId}/integration-operations/{operationId}/reconcile", c.reconcile)
	})
}

// ready 拒绝局部装配，防止写路径绕过 Store 或质量依赖。
func (c *WorkflowQualityController) ready(w http.ResponseWriter, r *http.Request) bool {
	if c.Store == nil || c.Workflows == nil || c.Artifacts == nil || c.Gates == nil || c.Reviews == nil || c.Commits == nil || c.Integrations == nil || c.Workflows.ExecutionFence == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return false
	}
	return true
}

// identity 只接受现有解析器证明的 Workflow 身份；请求体不能提供执行资格。
func (c *WorkflowQualityController) identity(w http.ResponseWriter, r *http.Request) (principal.Caller, bool) {
	if !c.ready(w, r) {
		return principal.Caller{}, false
	}
	caller, ok := c.Workflows.caller(w, r, principal.RequestFacts{WorkflowID: domain.WorkflowID(chi.URLParam(r, "workflowId")), RouteAttemptID: domain.WorkflowAttemptID(chi.URLParam(r, "attemptId"))})
	if !ok {
		return caller, false
	}
	_, found, err := c.Store.GetWorkflow(r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")))
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return caller, false
	}
	if !found {
		envelope.WriteError(w, r, qualityError(domain.ErrCollaborationWorkflowNotFound))
		return caller, false
	}
	return caller, true
}

// fence 以当前 Scheduler 读回任期，服务和 SQLite 仍会在副作用前复验。
func (c *WorkflowQualityController) fence(w http.ResponseWriter, r *http.Request) (domain.SchedulerFence, bool) {
	f, err := c.Workflows.ExecutionFence(r.Context())
	if err != nil {
		envelope.WriteError(w, r, schedulerError(err))
		return f, false
	}
	return f, true
}

// artifactTarget 只允许本 Workflow 的工件；写作者动作还必须匹配自己的 Attempt。
func (c *WorkflowQualityController) artifactTarget(w http.ResponseWriter, r *http.Request, caller principal.Caller, authorOnly bool) (domain.ArtifactSnapshot, bool) {
	a, found, err := c.Store.GetArtifact(r.Context(), chi.URLParam(r, "artifactId"))
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return a, false
	}
	if !found || a.WorkflowID != domain.WorkflowID(chi.URLParam(r, "workflowId")) {
		envelope.WriteError(w, r, apierr.NotFound("ARTIFACT_NOT_FOUND", "No artifact in this workflow"))
		return a, false
	}
	if authorOnly && caller.IsAgent() && (caller.AttemptID != a.AttemptID || caller.SessionID != a.SessionID) {
		envelope.WriteError(w, r, apierr.Forbidden("COLLAB_NOT_AUTHORIZED", "The caller does not own this artifact"))
		return a, false
	}
	return a, true
}

// snapshot 由 URL 对应的服务端 permit 冻结工件，绝不接收外部 SHA 或路径。
func (c *WorkflowQualityController) snapshot(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.identity(w, r); !ok {
		return
	}
	var req QualitySnapshotRequest
	if !qualityDecode(w, r, &req) {
		return
	}
	permit, ok := c.Workflows.executionPermit(w, r)
	if !ok {
		return
	}
	items, err := c.Artifacts.Snapshot(r.Context(), permit.AttemptID, req.ArtifactRevision, permit.Fence)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, QualityArtifactsResponse{Artifacts: items})
}

// gate 只执行已批准的服务端策略，Agent 不能提交 PASS 或命令行。
func (c *WorkflowQualityController) gate(w http.ResponseWriter, r *http.Request) {
	caller, ok := c.identity(w, r)
	if !ok {
		return
	}
	a, ok := c.artifactTarget(w, r, caller, true)
	if !ok {
		return
	}
	var req QualityActionRequest
	if !qualityDecode(w, r, &req) {
		return
	}
	f, ok := c.fence(w, r)
	if !ok {
		return
	}
	run, err := c.Gates.Run(r.Context(), req.IdempotencyKey, a.ID, f)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, qualityGateResponse(run))
}

// review 使用 principal 中的独立 Reviewer 身份，不能从正文指定 Actor。
func (c *WorkflowQualityController) review(w http.ResponseWriter, r *http.Request) {
	caller, ok := c.identity(w, r)
	if !ok {
		return
	}
	a, ok := c.artifactTarget(w, r, caller, false)
	if !ok {
		return
	}
	var req QualityActionRequest
	if !qualityDecode(w, r, &req) {
		return
	}
	f, ok := c.fence(w, r)
	if !ok {
		return
	}
	result, err := c.Reviews.Begin(r.Context(), req.IdempotencyKey, a.ID, caller, f)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, QualityReviewResponse{Review: publicQualityReview(result)})
}

// finishReview 先核对 Review 所属 Workflow，再交服务核验 Reviewer、目标 SHA 和 revision。
func (c *WorkflowQualityController) finishReview(w http.ResponseWriter, r *http.Request) {
	caller, ok := c.identity(w, r)
	if !ok {
		return
	}
	var req QualityReviewFinishRequest
	if !qualityDecode(w, r, &req) {
		return
	}
	review, found, err := c.Store.GetQualityReview(r.Context(), chi.URLParam(r, "reviewId"))
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	if !found {
		envelope.WriteError(w, r, apierr.NotFound("REVIEW_NOT_FOUND", "No review"))
		return
	}
	a, found, err := c.Store.GetArtifact(r.Context(), review.ArtifactID)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	if !found || a.WorkflowID != domain.WorkflowID(chi.URLParam(r, "workflowId")) {
		envelope.WriteError(w, r, apierr.NotFound("REVIEW_NOT_FOUND", "No review in this workflow"))
		return
	}
	f, ok := c.fence(w, r)
	if !ok {
		return
	}
	result, err := c.Reviews.Finish(r.Context(), review.ID, req.ExpectedRevision, req.Verdict, req.Findings, caller, f)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, QualityReviewResponse{Review: publicQualityReview(result)})
}

// commit 只请求既有 TaskCommit 服务；目标分支集成仍走独立 Approval。
func (c *WorkflowQualityController) commit(w http.ResponseWriter, r *http.Request) {
	caller, ok := c.identity(w, r)
	if !ok {
		return
	}
	a, ok := c.artifactTarget(w, r, caller, true)
	if !ok {
		return
	}
	var req QualityActionRequest
	if !qualityDecode(w, r, &req) {
		return
	}
	f, ok := c.fence(w, r)
	if !ok {
		return
	}
	result, err := c.Commits.Commit(r.Context(), req.IdempotencyKey, a.ID, req.ArtifactRevision, f)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, QualityCommitResponse{Commit: result})
}

// qualityDecode 对所有新正文使用严格字段和有界 JSON 解码。
func qualityDecode(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, principal.MaxApprovalPayloadBytes)
	if err := decodeJSONStrict(r, out); err != nil {
		envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Invalid quality request", nil))
		return false
	}
	return true
}

// qualityError 只返回安全原因码；Git 错误、路径或进程日志不进入错误正文。
func qualityError(err error) error {
	if _, ok := principal.AsApprovalRefusal(err); ok {
		return principalError(err)
	}
	switch {
	case errors.Is(err, domain.ErrCollaborationWorkflowNotFound):
		return apierr.NotFound("WORKFLOW_NOT_FOUND", "No workflow")
	case errors.Is(err, domain.ErrArtifactInvalid):
		return apierr.Invalid("ARTIFACT_INVALID", "Invalid artifact request", nil)
	case errors.Is(err, domain.ErrArtifactStale):
		return apierr.Conflict("ARTIFACT_STALE", "Evidence or revision changed", nil)
	case errors.Is(err, domain.ErrDeliveryBlocked):
		return apierr.Conflict("DELIVERY_BLOCKED", "Delivery evidence or approval is unavailable", nil)
	case errors.Is(err, domain.ErrSchedulerFence):
		return schedulerError(err)
	default:
		return apierr.Conflict("QUALITY_OPERATION_FAILED", "The quality operation could not be verified; inspect persisted evidence", nil)
	}
}
