package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	schedulersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/scheduler"
)

// SchedulerService 只提供观测和批准后的策略控制，不向 HTTP 暴露续租或调度权限。
type SchedulerService interface {
	Status(context.Context) (schedulersvc.Status, error)
	WorkflowStatus(context.Context, domain.WorkflowID) (domain.SchedulerWorkflowState, error)
	PutPolicy(context.Context, domain.SchedulerPolicy, int64, domain.PolicyChange, principal.Caller) (domain.SchedulerPolicy, bool, error)
	SetWorkflowPaused(context.Context, domain.WorkflowID, bool, int64, domain.PolicyChange, principal.Caller) (domain.SchedulerWorkflowState, bool, error)
	Decisions(context.Context, int64, int64) ([]domain.SchedulerDecision, error)
}

// BudgetService 读取 canonical 费用事实，HTTP 没有费用写入口。
type BudgetService interface {
	Policies(context.Context) ([]domain.BudgetPolicy, error)
	Snapshot(context.Context, string) (domain.BudgetSnapshot, error)
	PutPolicy(context.Context, domain.BudgetPolicy, int64, domain.PolicyChange, principal.Caller) (domain.BudgetPolicy, bool, error)
}

// SchedulerController 复用 Workflow 的批准边界；策略审计由服务的写事务持久化。
type SchedulerController struct {
	Svc       SchedulerService
	Budget    BudgetService
	Workflows *WorkflowsController
}

// Register 挂载固定控制路由，不能由正文扩张预算作用域或选择内部调度动作。
func (c *SchedulerController) Register(r chi.Router) {
	r.Get("/scheduler/status", c.status)
	r.Put("/scheduler/policy", c.putPolicy)
	r.Get("/scheduler/decisions", c.decisions)
	r.Get("/workflows/{workflowId}/scheduler", c.workflowStatus)
	r.Post("/workflows/{workflowId}/pause", func(w http.ResponseWriter, r *http.Request) { c.setPaused(w, r, true) })
	r.Post("/workflows/{workflowId}/resume-scheduling", func(w http.ResponseWriter, r *http.Request) { c.setPaused(w, r, false) })
	for _, path := range []string{"/scheduler/budget", "/workflows/{workflowId}/budget", "/workflows/{workflowId}/tasks/{taskId}/budget"} {
		r.Get(path, c.budget)
		r.Put(path, c.putBudget)
	}
}

// status 投影资源与恢复事实，不把内部租约或领导者身份外发。
func (c *SchedulerController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	status, err := c.Svc.Status(r.Context())
	if err != nil {
		envelope.WriteError(w, r, schedulerError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, schedulerStatusResponse(status))
}

// schedulerStatusResponse 供原 Scheduler 路由与协调摘要共用同一投影，避免显示语义漂移。
func schedulerStatusResponse(status schedulersvc.Status) SchedulerStatusResponse {
	response := SchedulerStatusResponse{Epoch: status.Leader.Fence.Epoch, LeaderLeaseUntil: status.Leader.LeaseUntil, Recovered: status.Leader.Recovered,
		ObservedAt: status.Resources.ObservedAt, TotalBytes: status.Resources.TotalBytes, AvailableBytes: status.Resources.AvailableBytes,
		Limit: status.Capacity.Limit, MaxNewClaims: status.Capacity.MaxNewClaims, ReasonCode: status.Capacity.ReasonCode, Occupied: status.Occupied,
		ByHarness: map[string]int64{}, Policies: make([]SchedulerPolicyResponse, 0, len(status.Policies))}
	for harness, count := range status.ByHarness {
		response.ByHarness[string(harness)] = count
	}
	for _, policy := range status.Policies {
		response.Policies = append(response.Policies, schedulerPolicyResponse(policy))
	}
	return response
}

// putPolicy 只选择全局或一个已知 Harness，服务再次验证 Operator 与 revision。
func (c *SchedulerController) putPolicy(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	var req PutSchedulerPolicyRequest
	caller, ok := c.Workflows.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	p := domain.DefaultSchedulerPolicy(domain.AgentHarness(req.Harness))
	p.ResourceLimitsDisabled = req.ResourceLimitsDisabled
	p.MaxConcurrent, p.CPURecoverPercent, p.CPUSoftPercent, p.CPUHardPercent = req.MaxConcurrent, req.CPURecoverPercent, req.CPUSoftPercent, req.CPUHardPercent
	p.RAMSoftPercent, p.RAMHardPercent, p.RAMFloorBytes, p.Revision = req.RAMSoftPercent, req.RAMHardPercent, req.RAMFloorBytes, req.ExpectedRevision
	policy, applied, err := c.Svc.PutPolicy(r.Context(), p, req.ExpectedRevision, policyChange(r, req.IdempotencyKey, req.Reason, caller), caller)
	if err != nil {
		envelope.WriteError(w, r, schedulerError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SchedulerPolicyEnvelope{Policy: schedulerPolicyResponse(policy), Applied: applied})
}

// workflowStatus 返回该 Workflow 的调度状态，不把缺失行误报为可调度。
func (c *SchedulerController) workflowStatus(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	state, err := c.Svc.WorkflowStatus(r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")))
	if err != nil {
		envelope.WriteError(w, r, schedulerError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workflowSchedulerResponse(state))
}

// setPaused 仅修改新任务准入，已有 Session 的停止仍走独立人工操作。
func (c *SchedulerController) setPaused(w http.ResponseWriter, r *http.Request, paused bool) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	var req OperatorChangeRequest
	caller, ok := c.Workflows.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	state, _, err := c.Svc.SetWorkflowPaused(r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")), paused, req.ExpectedRevision, policyChange(r, req.IdempotencyKey, req.Reason, caller), caller)
	if err != nil {
		envelope.WriteError(w, r, schedulerError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workflowSchedulerResponse(state))
}

// decisions 对审计分页设置硬上限，恶意游标不能触发无界读取。
func (c *SchedulerController) decisions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	after, limit := int64(0), int64(100)
	var err error
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
	}
	if err == nil {
		if raw := r.URL.Query().Get("limit"); raw != "" {
			limit, err = strconv.ParseInt(raw, 10, 64)
		}
	}
	if err != nil || after < 0 || limit < 1 || limit > 500 {
		envelope.WriteError(w, r, apierr.Invalid("INVALID_QUERY", "Invalid scheduler decision page", nil))
		return
	}
	decisions, err := c.Svc.Decisions(r.Context(), after, limit)
	if err != nil {
		envelope.WriteError(w, r, schedulerError(err))
		return
	}
	response := SchedulerDecisionsResponse{Decisions: make([]SchedulerDecisionResponse, 0, len(decisions))}
	for _, d := range decisions {
		response.Decisions = append(response.Decisions, SchedulerDecisionResponse{ID: d.ID, AdmissionID: d.AdmissionID, WorkflowID: string(d.WorkflowID), TaskID: string(d.TaskID), Epoch: d.Fence.Epoch, ReasonCode: d.ReasonCode, PolicyRevisions: d.PolicyRevisions, CreatedAt: d.CreatedAt})
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

// budgetScope 先验证 URL 中的 Task 归属，避免用另一 Workflow 的 TaskID 修改预算。
func (c *SchedulerController) budgetScope(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	if workflowID == "" {
		return "global", "", true
	}
	if c.Workflows == nil || c.Workflows.Svc == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return "", "", false
	}
	if taskID := domain.WorkflowTaskID(chi.URLParam(r, "taskId")); taskID != "" {
		task, err := c.Workflows.Svc.GetTask(r.Context(), workflowID, taskID)
		if err != nil {
			envelope.WriteError(w, r, err)
			return "", "", false
		}
		if task.WorkflowID != workflowID || task.ID != taskID {
			envelope.WriteError(w, r, apierr.NotFound("WORKFLOW_TASK_NOT_FOUND", "No such task in this workflow"))
			return "", "", false
		}
		return "task", string(taskID), true
	}
	if _, err := c.Workflows.Svc.Get(r.Context(), workflowID); err != nil {
		envelope.WriteError(w, r, err)
		return "", "", false
	}
	return "workflow", string(workflowID), true
}

// budget 只列 URL 作用域的策略及各自快照，保留 nil 费用的未知含义。
func (c *SchedulerController) budget(w http.ResponseWriter, r *http.Request) {
	if c.Budget == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	scope, scopeID, ok := c.budgetScope(w, r)
	if !ok {
		return
	}
	policies, err := c.Budget.Policies(r.Context())
	if err != nil {
		envelope.WriteError(w, r, schedulerError(err))
		return
	}
	response := BudgetSnapshotsResponse{Policies: []BudgetPolicyResponse{}, Snapshots: []BudgetSnapshotResponse{}}
	for _, p := range policies {
		if p.Scope != scope || p.ScopeID != scopeID {
			continue
		}
		snapshot, err := c.Budget.Snapshot(r.Context(), p.ID)
		if err != nil {
			envelope.WriteError(w, r, schedulerError(err))
			return
		}
		if snapshot.Policy.Scope != scope || snapshot.Policy.ScopeID != scopeID {
			envelope.WriteError(w, r, apierr.Conflict("BUDGET_POLICY_CHANGED", "The budget policy changed during this read", nil))
			return
		}
		response.Policies = append(response.Policies, budgetPolicyResponse(snapshot.Policy))
		response.Snapshots = append(response.Snapshots, budgetSnapshotResponse(snapshot))
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

// putBudget 的作用域来自路由，正文即使是合法 JSON 也无权提供其他 scope。
func (c *SchedulerController) putBudget(w http.ResponseWriter, r *http.Request) {
	if c.Budget == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	var req PutBudgetPolicyRequest
	caller, ok := c.Workflows.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	scope, scopeID, ok := c.budgetScope(w, r)
	if !ok {
		return
	}
	p := domain.BudgetPolicy{ID: req.ID, Scope: scope, ScopeID: scopeID, Currency: req.Currency, Mode: req.Mode, Period: req.Period, LimitNanos: req.LimitNanos, ReservationNanos: req.ReservationNanos, ReservationBasis: req.ReservationBasis, ReservationRef: req.ReservationRef, Revision: req.ExpectedRevision}
	policy, applied, err := c.Budget.PutPolicy(r.Context(), p, req.ExpectedRevision, policyChange(r, req.IdempotencyKey, req.Reason, caller), caller)
	if err != nil {
		envelope.WriteError(w, r, schedulerError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, BudgetPolicyEnvelope{Policy: budgetPolicyResponse(policy), Applied: applied})
}

// policyChange 的指纹与一次性批准来自同一原始正文；主体和时间始终由 daemon 派生。
func policyChange(r *http.Request, key, reason string, caller principal.Caller) domain.PolicyChange {
	fingerprint, _ := r.Context().Value(approvalBodyDigestKey{}).(string)
	return domain.PolicyChange{IdempotencyKey: key, RequestFingerprint: fingerprint, ActorRef: caller.OperatorTokenFingerprint, Reason: reason, Now: time.Now().UTC()}
}

// schedulerError 将控制面的安全拒绝保留为稳定错误码，不泄漏内部许可。
func schedulerError(err error) error {
	switch {
	case errors.Is(err, principal.ErrNotAuthorized):
		return principalError(err)
	case errors.Is(err, domain.ErrSchedulerFence):
		return apierr.Conflict("SCHEDULER_FENCE_STALE", "Scheduler ownership is stale or not recovered", nil)
	case errors.Is(err, domain.ErrSchedulerPolicy):
		return apierr.Conflict("SCHEDULER_POLICY_CONFLICT", "Scheduler policy or revision is invalid", nil)
	case errors.Is(err, domain.ErrSchedulerAdmissionConflict):
		return apierr.Conflict("SCHEDULER_ADMISSION_CONFLICT", "Execution admission does not match durable state", nil)
	case errors.Is(err, domain.ErrAdmissionRequired):
		return apierr.Conflict("SCHEDULER_ADMISSION_REQUIRED", "Execution requires current admission evidence", nil)
	case errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict):
		return apierr.Conflict("WORKFLOW_ATTEMPT_LEASE_CONFLICT", "The attempt lease no longer matches durable state", nil)
	case errors.Is(err, domain.ErrSchedulerCapacity):
		return apierr.Conflict("SCHEDULER_CAPACITY_EXHAUSTED", "Scheduler capacity is unavailable", nil)
	case errors.Is(err, domain.ErrBudgetPolicy):
		return apierr.Conflict("BUDGET_POLICY_CONFLICT", "Budget policy or revision is invalid", nil)
	case errors.Is(err, domain.ErrBudgetExceeded):
		return apierr.Conflict("BUDGET_EXCEEDED", "Budget cannot admit another execution", nil)
	case errors.Is(err, domain.ErrBudgetUnknown), errors.Is(err, domain.ErrBudgetOverflow):
		return apierr.Conflict("BUDGET_UNKNOWN", "Budget evidence is unknown or out of range", nil)
	}
	return err
}

func schedulerPolicyResponse(p domain.SchedulerPolicy) SchedulerPolicyResponse {
	return SchedulerPolicyResponse{ID: p.ID, Scope: p.Scope, ScopeID: p.ScopeID, MaxConcurrent: p.MaxConcurrent, ResourceLimitsDisabled: p.ResourceLimitsDisabled, CPURecoverPercent: p.CPURecoverPercent, CPUSoftPercent: p.CPUSoftPercent, CPUHardPercent: p.CPUHardPercent, RAMSoftPercent: p.RAMSoftPercent, RAMHardPercent: p.RAMHardPercent, RAMFloorBytes: p.RAMFloorBytes, Revision: p.Revision}
}
func workflowSchedulerResponse(s domain.SchedulerWorkflowState) WorkflowSchedulerResponse {
	return WorkflowSchedulerResponse{WorkflowID: string(s.WorkflowID), Paused: s.Paused, Revision: s.Revision, CandidateOffset: s.CandidateOffset, Deficit: s.Deficit, Weight: s.Weight}
}
func budgetPolicyResponse(p domain.BudgetPolicy) BudgetPolicyResponse {
	return BudgetPolicyResponse{ID: p.ID, Scope: p.Scope, ScopeID: p.ScopeID, Currency: p.Currency, Mode: p.Mode, Period: p.Period, LimitNanos: p.LimitNanos, ReservationNanos: p.ReservationNanos, ReservationBasis: p.ReservationBasis, ReservationRef: p.ReservationRef, Revision: p.Revision}
}
func budgetSnapshotResponse(s domain.BudgetSnapshot) BudgetSnapshotResponse {
	return BudgetSnapshotResponse{Policy: budgetPolicyResponse(s.Policy), Window: s.Window, KnownCostNanos: s.KnownCostNanos, CostNanos: s.CostNanos, ReservedNanos: s.ReservedNanos, RemainingNanos: s.RemainingNanos, Quality: s.Quality, Tokens: BudgetTokenMetricsResponse{InputTokens: s.Tokens.InputTokens, CachedInputTokens: s.Tokens.CachedInputTokens, UncachedInputTokens: s.Tokens.UncachedInputTokens, OutputTokens: s.Tokens.OutputTokens}, EventCount: s.EventCount, AlertThreshold: s.AlertThreshold, ReconcilePending: s.ReconcilePending}
}
