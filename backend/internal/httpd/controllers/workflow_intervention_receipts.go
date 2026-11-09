package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// WorkflowInterventionReceiptsResponse 保留原始状态与幂等键供页面恢复。
type WorkflowInterventionReceiptsResponse struct {
	Interventions []WorkflowInterventionResponse `json:"interventions"`
}

// WorkflowInterventionReceiptReader 只读取持久状态，不复用发送操作恢复页面。
type WorkflowInterventionReceiptReader interface {
	ListReceipts(context.Context, domain.WorkflowID, domain.WorkflowTaskID, domain.WorkflowAttemptID) ([]domain.WorkflowIntervention, error)
}

// interventionReceipts 先验证身份和持久归属，不能跨工作流读取其他 Attempt 回执。
func (c *WorkflowsController) interventionReceipts(w http.ResponseWriter, r *http.Request) {
	wid := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	aid := domain.WorkflowAttemptID(chi.URLParam(r, "attemptId"))
	if _, ok := c.caller(w, r, principal.RequestFacts{WorkflowID: wid, RouteAttemptID: aid}); !ok {
		return
	}
	binding, ok := c.requireAttemptInWorkflow(w, r, wid, aid)
	if !ok {
		return
	}
	reader, ok := c.Interventions.(WorkflowInterventionReceiptReader)
	if !ok {
		envelope.WriteError(w, r, apierr.NotImplemented("INTERVENTION_RECEIPTS_UNAVAILABLE", "干预回执读取未装配"))
		return
	}
	rows, err := reader.ListReceipts(r.Context(), wid, binding.TaskID, aid)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	response := WorkflowInterventionReceiptsResponse{Interventions: make([]WorkflowInterventionResponse, 0, len(rows))}
	for _, row := range rows {
		response.Interventions = append(response.Interventions, workflowInterventionResponse(row))
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}
