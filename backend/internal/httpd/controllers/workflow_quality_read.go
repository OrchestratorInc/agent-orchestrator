package controllers

import (
	"context"
	"net/http"
	"sort"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/go-chi/chi/v5"
)

// policies 列出可选可信策略及工作流当前 CAS 版本，不输出 executable、argv 或 env。
func (c *WorkflowQualityController) policies(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.identity(w, r); !ok {
		return
	}
	current, err := c.currentPolicy(r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")))
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	var revision int64
	if current != nil {
		revision = current.Revision
	}
	out := QualityPoliciesResponse{Policies: []QualityPolicyResponse{}}
	for _, p := range c.Gates.Registry {
		out.Policies = append(out.Policies, QualityPolicyResponse{ID: p.ID, PolicyRevision: p.Revision, Revision: revision, Digest: p.Digest()})
	}
	sort.Slice(out.Policies, func(i, j int) bool { return out.Policies[i].ID < out.Policies[j].ID })
	envelope.WriteJSON(w, http.StatusOK, out)
}

// configure 只有人工可以选择服务端已登记策略；正文无法注入新的命令或环境。
func (c *WorkflowQualityController) configure(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	var req QualityPolicyRequest
	caller, ok := c.Workflows.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	f, ok := c.fence(w, r)
	if !ok {
		return
	}
	p, err := c.Gates.Configure(r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")), req.PolicyID, req.ExpectedRevision, caller, f)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, QualityPolicyResponse{ID: p.Policy.ID, PolicyRevision: p.Policy.Revision, Revision: p.Revision, Digest: p.Digest})
}

// currentPolicy 读取已保存的策略快照；Registry 更新或移除不能改写当前选择及其 CAS 版本。
func (c *WorkflowQualityController) currentPolicy(ctx context.Context, w domain.WorkflowID) (*QualityPolicyResponse, error) {
	p, found, err := c.Store.GetWorkflowQualityPolicy(ctx, w)
	if err != nil || !found {
		return nil, err
	}
	return &QualityPolicyResponse{ID: p.Policy.ID, PolicyRevision: p.Policy.Revision, Revision: p.Revision, Digest: p.Digest}, nil
}

// qualityGateResponse 白名单投影，原始日志永远不会被复制到 HTTP 响应。
func qualityGateResponse(g domain.GateRun) QualityGateResponse {
	out := QualityGateResponse{ID: g.ID, ArtifactID: g.ArtifactID, ArtifactFingerprint: g.ArtifactFingerprint, PolicyDigest: g.PolicyDigest, Fingerprint: g.Fingerprint, State: g.State, Revision: g.Revision, CreatedAt: g.CreatedAt, ExpiresAt: g.ExpiresAt, Results: []QualityGateResultResponse{}}
	for _, r := range g.Results {
		out.Results = append(out.Results, QualityGateResultResponse{RuleID: r.RuleID, State: r.State, Reason: r.Reason, ExitCode: r.ExitCode, LogDigest: r.LogDigest, Truncated: r.Truncated, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt})
	}
	return out
}

// publicQualityReview 不泄露内部 generation；无 findings 使用明确空数组。
func publicQualityReview(r domain.QualityReview) domain.QualityReview {
	r.Actor.Generation = ""
	if r.Findings == nil {
		r.Findings = []domain.QualityFinding{}
	}
	return r
}

// qualityFacts 查询全部已有交付侧车，不推断代码已完成或集成已获批准。
func (c *WorkflowQualityController) qualityFacts(ctx context.Context, w domain.WorkflowID) (WorkflowQualitySummaryResponse, error) {
	out := WorkflowQualitySummaryResponse{Gates: []QualityGateResponse{}}
	var err error
	out.CurrentPolicy, err = c.currentPolicy(ctx, w)
	if err != nil {
		return out, err
	}
	out.Artifacts, err = c.Store.ListArtifacts(ctx, w)
	if err != nil {
		return out, err
	}
	gates, err := c.Store.ListGateRuns(ctx, w)
	if err != nil {
		return out, err
	}
	for _, g := range gates {
		out.Gates = append(out.Gates, qualityGateResponse(g))
	}
	out.Reviews, err = c.Store.ListQualityReviews(ctx, w)
	if err != nil {
		return out, err
	}
	for i := range out.Reviews {
		out.Reviews[i] = publicQualityReview(out.Reviews[i])
	}
	out.Commits, err = c.Store.ListTaskCommits(ctx, w)
	if err != nil {
		return out, err
	}
	out.Integrations, err = c.Store.ListIntegrationPlans(ctx, w)
	if err != nil {
		return out, err
	}
	out.Operations, err = c.Store.ListIntegrationOperations(ctx, w)
	out.Artifacts = qualityWindow(out.Artifacts, &out.Truncated)
	out.Gates = qualityWindow(out.Gates, &out.Truncated)
	out.Reviews = qualityWindow(out.Reviews, &out.Truncated)
	out.Commits = qualityWindow(out.Commits, &out.Truncated)
	out.Integrations = qualityWindow(out.Integrations, &out.Truncated)
	out.Operations = qualityWindow(out.Operations, &out.Truncated)
	return out, err
}

// qualityWindow 明确标记有界历史，不能把裁切后的证据列表描述为完整验收。
func qualityWindow[T any](items []T, truncated *bool) []T {
	if len(items) > 500 {
		*truncated = true
		return items[:500]
	}
	if items == nil {
		return []T{}
	}
	return items
}

// summary 在身份验证后只读聚合各质量模块；没有新的可写质量状态。
func (c *WorkflowQualityController) summary(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.identity(w, r); !ok {
		return
	}
	out, err := c.qualityFacts(r.Context(), domain.WorkflowID(chi.URLParam(r, "workflowId")))
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

// coordination 将已有 Session 派生状态、预算快照与质量侧车一起返回，不执行 Probe 或派发。
func (c *WorkflowQualityController) coordination(w http.ResponseWriter, r *http.Request) {
	if _, ok := c.identity(w, r); !ok {
		return
	}
	if c.Coordination == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	wid := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	facts, err := c.Coordination.Read(r.Context(), wid)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	quality, err := c.qualityFacts(r.Context(), wid)
	if err != nil {
		envelope.WriteError(w, r, qualityError(err))
		return
	}
	out := WorkflowCoordinationResponse{Coordination: facts, Quality: quality, Budget: BudgetSnapshotsResponse{Policies: []BudgetPolicyResponse{}, Snapshots: []BudgetSnapshotResponse{}}}
	if c.Scheduler != nil && c.Scheduler.Svc != nil {
		status, err := c.Scheduler.Svc.Status(r.Context())
		if err != nil {
			envelope.WriteError(w, r, schedulerError(err))
			return
		}
		view := schedulerStatusResponse(status)
		out.Scheduler = &view
	}
	if c.Scheduler != nil && c.Scheduler.Budget != nil {
		policies, err := c.Scheduler.Budget.Policies(r.Context())
		if err != nil {
			envelope.WriteError(w, r, schedulerError(err))
			return
		}
		for _, p := range policies {
			if p.Scope != "workflow" || p.ScopeID != string(wid) {
				continue
			}
			snapshot, err := c.Scheduler.Budget.Snapshot(r.Context(), p.ID)
			if err != nil {
				envelope.WriteError(w, r, schedulerError(err))
				return
			}
			out.Budget.Policies = append(out.Budget.Policies, budgetPolicyResponse(snapshot.Policy))
			out.Budget.Snapshots = append(out.Budget.Snapshots, budgetSnapshotResponse(snapshot))
		}
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}
