package specgen

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	workflowsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// planningOperations 由正式 handler 的请求与响应类型生成，不维护另一份计划协议。
func planningOperations() []operation {
	errors := []respUnit{{400, envelope.APIError{}}, {403, envelope.APIError{}}, {404, envelope.APIError{}}, {409, envelope.APIError{}}, {500, envelope.APIError{}}, {501, envelope.APIError{}}}
	return []operation{
		{method: "GET", path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/capabilities", id: "getWorkflowAttemptCapabilities", tag: "workflows", summary: "Read negotiated capabilities for the bound session", pathParams: []any{controllers.WorkflowAttemptIDParam{}}, resps: append([]respUnit{{200, controllers.GetWorkflowCapabilitiesResponse{}}}, errors...)},
		{method: "GET", path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/interventions", id: "listWorkflowInterventionReceipts", tag: "workflows", summary: "Read durable intervention receipts for an attempt", pathParams: []any{controllers.WorkflowAttemptIDParam{}}, resps: append([]respUnit{{200, controllers.WorkflowInterventionReceiptsResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/replan", id: "startWorkflowReplan", tag: "workflows", summary: "Start a new planner after trusted stop", pathParams: []any{controllers.WorkflowIDParam{}}, reqBody: workflowsvc.ReplanInput{}, resps: append([]respUnit{{200, controllers.WorkflowTaskEnvelope{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/plan", id: "startWorkflowPlan", tag: "workflows", summary: "Start a read-only planning task", reqBody: controllers.StartWorkflowPlanRequest{}, resps: append([]respUnit{{200, controllers.WorkflowEnvelope{}}}, errors...)},
		{method: "GET", path: "/api/v1/workflows/{workflowId}/plan-candidates", id: "listWorkflowPlans", tag: "workflows", summary: "List durable plan candidates", pathParams: []any{controllers.WorkflowIDParam{}}, resps: append([]respUnit{{200, controllers.WorkflowPlansResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/plan-candidates", id: "prepareWorkflowPlan", tag: "workflows", summary: "Validate a completed planner turn", pathParams: []any{controllers.WorkflowIDParam{}}, reqBody: workflowsvc.PlanInput{}, resps: append([]respUnit{{200, controllers.WorkflowPlanResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/plan-candidates/{candidateId}/confirm", id: "confirmWorkflowPlan", tag: "workflows", summary: "Confirm a candidate and pause scheduling", pathParams: []any{controllers.WorkflowPlanCandidateIDParam{}}, reqBody: controllers.ConfirmWorkflowPlanRequest{}, resps: append([]respUnit{{200, controllers.WorkflowPlanResponse{}}}, errors...)},
	}
}
