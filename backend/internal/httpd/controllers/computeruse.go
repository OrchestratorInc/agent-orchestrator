package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/computeruse"
)

// ComputerUse is the controller-facing contract for the Cua Driver integration.
type ComputerUse interface {
	Status(ctx context.Context) (computeruse.Status, error)
	StartDaemon(ctx context.Context) (computeruse.Status, error)
	GrantPermissions(ctx context.Context) (computeruse.Status, error)
	Doctor(ctx context.Context) (string, error)
	AgentTools(ctx context.Context) ([]computeruse.AgentTool, error)
	Call(ctx context.Context, request computeruse.CallRequest) (computeruse.CallResult, error)
}

// ComputerUseController owns the /computer-use routes behind Settings and the
// computer-use tools.
type ComputerUseController struct {
	Svc ComputerUse
}

// Register mounts the computer-use routes on the supplied router.
func (c *ComputerUseController) Register(r chi.Router) {
	r.Get("/computer-use/status", c.status)
	r.Post("/computer-use/daemon/start", c.startDaemon)
	r.Post("/computer-use/permissions/grant", c.grantPermissions)
	r.Get("/computer-use/doctor", c.doctor)
	r.Get("/computer-use/agent-tools", c.agentTools)
	r.Post("/computer-use/call", c.call)
}

func (c *ComputerUseController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/computer-use/status")
		return
	}
	writeComputerUse(w, r, http.StatusOK)(c.Svc.Status(r.Context()))
}

func (c *ComputerUseController) startDaemon(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/computer-use/daemon/start")
		return
	}
	writeComputerUse(w, r, http.StatusOK)(c.Svc.StartDaemon(r.Context()))
}

func (c *ComputerUseController) grantPermissions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/computer-use/permissions/grant")
		return
	}
	writeComputerUse(w, r, http.StatusAccepted)(c.Svc.GrantPermissions(r.Context()))
}

func (c *ComputerUseController) doctor(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/computer-use/doctor")
		return
	}
	report, err := c.Svc.Doctor(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ComputerUseDoctorResponse{Report: report})
}

func (c *ComputerUseController) agentTools(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/computer-use/agent-tools")
		return
	}
	tools, err := c.Svc.AgentTools(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ComputerUseAgentToolsResponse{Tools: tools})
}

func (c *ComputerUseController) call(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/computer-use/call")
		return
	}
	var request computeruse.CallRequest
	if err := decodeJSONStrict(r, &request); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_COMPUTER_USE_CALL", "invalid computer-use call", nil)
		return
	}
	result, err := c.Svc.Call(r.Context(), request)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}

func writeComputerUse(w http.ResponseWriter, r *http.Request, okStatus int) func(computeruse.Status, error) {
	return func(status computeruse.Status, err error) {
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		envelope.WriteJSON(w, okStatus, status)
	}
}
