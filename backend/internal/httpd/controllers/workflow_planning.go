package controllers

import (
	"context"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	workflowsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/go-chi/chi/v5"
)

// WorkflowPlanningService 复用现有服务，只在具备正式规划持久化的装配上启用。
type WorkflowPlanningService interface {
	StartReplan(context.Context, workflowsvc.ReplanInput) (domain.WorkflowTask, bool, error)
	StartPlan(context.Context, string, workflowsvc.CreateInput, workflowsvc.TaskInput) (domain.Workflow, bool, error)
	PreparePlan(context.Context, workflowsvc.PlanInput) (domain.WorkflowPlanCandidate, error)
	ConfirmPlan(context.Context, domain.WorkflowID, string, string, int64) (domain.WorkflowPlanCandidate, error)
	ListPlans(context.Context, domain.WorkflowID) ([]domain.WorkflowPlanCandidate, error)
}

// startReplan 授权新规划任务和可信停止收尾，不授权模型直接替换开发图。
func (c *WorkflowsController) startReplan(w http.ResponseWriter, r *http.Request) {
	s, ok := c.planningService(w, r)
	if !ok {
		return
	}
	var req workflowsvc.ReplanInput
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	req.WorkflowID = domain.WorkflowID(chi.URLParam(r, "workflowId"))
	task, created, err := s.StartReplan(r.Context(), req)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !c.recordMutation(w, r, caller, req.WorkflowID, "", req.IdempotencyKey, "start_replan", created) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowTaskEnvelope{Task: workflowTaskResponse(task), Attempts: []WorkflowAttemptResponse{}})
}

// StartWorkflowPlanRequest 不接受任务图，执行图只能通过候选确认进入。
type StartWorkflowPlanRequest struct {
	Title          string                    `json:"title"`
	Goal           string                    `json:"goal"`
	Repositories   []WorkflowRepositoryInput `json:"repositories"`
	AgentHarness   string                    `json:"agentHarness"`
	Model          string                    `json:"model"`
	Effort         string                    `json:"effort,omitempty"`
	Profile        string                    `json:"profile,omitempty"`
	IdempotencyKey string                    `json:"idempotencyKey"`
}

// ConfirmWorkflowPlanRequest 精确绑定用户预览的候选、摘要和工作流 revision。
type ConfirmWorkflowPlanRequest struct {
	Digest           string `json:"digest"`
	ExpectedRevision int64  `json:"expectedRevision"`
}
type WorkflowPlanCandidateIDParam struct {
	WorkflowID  string `path:"workflowId"`
	CandidateID string `path:"candidateId"`
}
type WorkflowPlanResponse struct {
	Candidate domain.WorkflowPlanCandidate `json:"candidate"`
}
type WorkflowPlansResponse struct {
	Candidates []domain.WorkflowPlanCandidate `json:"candidates"`
}

// planningService 缺装配时明确拒绝，不能把 UI 计划当已持久化任务。
func (c *WorkflowsController) planningService(w http.ResponseWriter, r *http.Request) (WorkflowPlanningService, bool) {
	s, ok := c.Svc.(WorkflowPlanningService)
	if !ok {
		envelope.WriteError(w, r, apierr.NotImplemented("PLAN_UNAVAILABLE", "规划服务未装配"))
	}
	return s, ok
}

// startPlan 仅创建受控只读规划任务，激活仍使用统一调度入口。
func (c *WorkflowsController) startPlan(w http.ResponseWriter, r *http.Request) {
	s, ok := c.planningService(w, r)
	if !ok {
		return
	}
	var req StartWorkflowPlanRequest
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	in := workflowCreateInput(CreateWorkflowRequest{Title: req.Title, Goal: req.Goal, Repositories: req.Repositories})
	rec, created, err := s.StartPlan(r.Context(), req.IdempotencyKey, in, workflowsvc.TaskInput{AgentHarness: domain.AgentHarness(req.AgentHarness), Model: req.Model, Effort: req.Effort, Profile: req.Profile})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !c.recordMutation(w, r, caller, rec.ID, "", req.IdempotencyKey, "start_plan", created) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowEnvelope{Workflow: workflowResponse(rec)})
}

// preparePlan 读取持久 Chat 输出或明确编辑正文，二者使用同一校验边界。
func (c *WorkflowsController) preparePlan(w http.ResponseWriter, r *http.Request) {
	s, ok := c.planningService(w, r)
	if !ok {
		return
	}
	var req workflowsvc.PlanInput
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	req.WorkflowID = domain.WorkflowID(chi.URLParam(r, "workflowId"))
	p, err := s.PreparePlan(r.Context(), req)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !c.recordMutation(w, r, caller, req.WorkflowID, req.SourceAttemptID, req.IdempotencyKey, "prepare_plan", true) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowPlanResponse{Candidate: p})
}

// confirmPlan 在原生审批后执行原子摘要/CAS 校验；重放只返回原结果。
func (c *WorkflowsController) confirmPlan(w http.ResponseWriter, r *http.Request) {
	s, ok := c.planningService(w, r)
	if !ok {
		return
	}
	var req ConfirmWorkflowPlanRequest
	caller, ok := c.operatorWrite(w, r, &req)
	if !ok {
		return
	}
	id := chi.URLParam(r, "candidateId")
	workflowID := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	p, err := s.ConfirmPlan(r.Context(), workflowID, id, req.Digest, req.ExpectedRevision)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !c.recordMutation(w, r, caller, workflowID, p.SourceAttemptID, id, "confirm_plan", true) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowPlanResponse{Candidate: p})
}

// listPlans 与其他协作只读接口保持相同 Principal 边界。
func (c *WorkflowsController) listPlans(w http.ResponseWriter, r *http.Request) {
	s, ok := c.planningService(w, r)
	if !ok {
		return
	}
	id := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	if _, ok := c.caller(w, r, principal.RequestFacts{WorkflowID: id}); !ok {
		return
	}
	rows, err := s.ListPlans(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WorkflowPlansResponse{Candidates: rows})
}
