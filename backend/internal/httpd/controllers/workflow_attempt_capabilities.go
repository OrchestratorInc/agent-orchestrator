package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// attemptCapabilities 从持久绑定选择 Session，代际和协商能力仍由真实 Controller 观测。
func (c *WorkflowsController) attemptCapabilities(w http.ResponseWriter, r *http.Request) {
	wid := domain.WorkflowID(chi.URLParam(r, "workflowId"))
	aid := domain.WorkflowAttemptID(chi.URLParam(r, "attemptId"))
	if _, ok := c.caller(w, r, principal.RequestFacts{WorkflowID: wid, RouteAttemptID: aid}); !ok {
		return
	}
	binding, ok := c.requireAttemptInWorkflow(w, r, wid, aid)
	if !ok {
		return
	}
	if binding.SessionID == nil || *binding.SessionID == "" || binding.ControllerGeneration == "" {
		envelope.WriteError(w, r, apierr.Conflict("WORKFLOW_BIND_CONFLICT", "当前 Attempt 尚无可观测的会话绑定", nil))
		return
	}
	harness, ok := c.interventionHarness(w, r, wid, aid, "")
	if !ok {
		return
	}
	if c.Capabilities == nil {
		envelope.WriteError(w, r, apierr.NotImplemented("CAPABILITIES_UNAVAILABLE", "能力读取未装配"))
		return
	}
	snapshot, err := c.Capabilities.Resolve(r.Context(), ports.CapabilityProbeRequest{Harness: harness, SessionID: *binding.SessionID, Generation: binding.ControllerGeneration})
	if err != nil {
		envelope.WriteError(w, r, apierr.InternalCause("CAPABILITY_PROBE_FAILED", "无法读取当前会话能力", err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, GetWorkflowCapabilitiesResponse{Capabilities: []WorkflowCapabilityResponse{workflowCapabilityResponse(snapshot, c.now())}})
}
