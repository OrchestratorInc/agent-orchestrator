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

	RemoteSettings(ctx context.Context) (computeruse.RemoteSettings, error)
	SetAllowRemote(ctx context.Context, allow bool) (computeruse.RemoteSettings, error)
	SetRemoteTarget(ctx context.Context, request computeruse.SetRemoteTargetRequest) (computeruse.RemoteSettings, error)
	ClearRemoteTarget(ctx context.Context) (computeruse.RemoteSettings, error)
	RemoteOffer(ctx context.Context) (computeruse.RemoteOffer, error)
	RemoteAgentTools(ctx context.Context) ([]computeruse.AgentTool, error)
	ServeRemoteCall(ctx context.Context, request computeruse.RemoteCallRequest) (computeruse.RemoteCallResult, error)
}

// ComputerUseController owns the /computer-use routes behind Settings and the
// computer-use tools, and the /remote-computer-use routes agents on the user's
// other computers call. /computer-use is loopback-only; /remote-computer-use is
// also served, authenticated, on the remote-host listener, and refuses every
// call until the user lets other computers use this one.
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
	r.Get("/computer-use/remote", c.remoteSettings)
	r.Put("/computer-use/remote/allow", c.setAllowRemote)
	r.Put("/computer-use/remote/target", c.setRemoteTarget)
	r.Delete("/computer-use/remote/target", c.clearRemoteTarget)

	r.Get("/remote-computer-use/status", c.remoteOffer)
	r.Get("/remote-computer-use/agent-tools", c.remoteAgentTools)
	r.Post("/remote-computer-use/call", c.remoteCall)
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

func (c *ComputerUseController) remoteSettings(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/computer-use/remote")
		return
	}
	writeRemoteSettings(w, r)(c.Svc.RemoteSettings(r.Context()))
}

func (c *ComputerUseController) setAllowRemote(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/computer-use/remote/allow")
		return
	}
	var request UpdateComputerUseRemoteAllowRequest
	if err := decodeJSONStrict(r, &request); err != nil || request.Enabled == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "COMPUTER_USE_REMOTE_INVALID", "enabled must be true or false", nil)
		return
	}
	writeRemoteSettings(w, r)(c.Svc.SetAllowRemote(r.Context(), *request.Enabled))
}

func (c *ComputerUseController) setRemoteTarget(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/computer-use/remote/target")
		return
	}
	var request SetComputerUseRemoteTargetRequest
	if err := decodeJSONStrict(r, &request); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "COMPUTER_USE_REMOTE_INVALID", "invalid remote computer-use target", nil)
		return
	}
	writeRemoteSettings(w, r)(c.Svc.SetRemoteTarget(r.Context(), request))
}

func (c *ComputerUseController) clearRemoteTarget(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/computer-use/remote/target")
		return
	}
	writeRemoteSettings(w, r)(c.Svc.ClearRemoteTarget(r.Context()))
}

func (c *ComputerUseController) remoteOffer(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/remote-computer-use/status")
		return
	}
	offer, err := c.Svc.RemoteOffer(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, offer)
}

func (c *ComputerUseController) remoteAgentTools(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/remote-computer-use/agent-tools")
		return
	}
	tools, err := c.Svc.RemoteAgentTools(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ComputerUseAgentToolsResponse{Tools: tools})
}

func (c *ComputerUseController) remoteCall(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/remote-computer-use/call")
		return
	}
	var request RemoteComputerUseCallRequest
	if err := decodeJSONStrict(r, &request); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_COMPUTER_USE_CALL", "invalid computer-use call", nil)
		return
	}
	result, err := c.Svc.ServeRemoteCall(r.Context(), request)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}

func writeRemoteSettings(w http.ResponseWriter, r *http.Request) func(computeruse.RemoteSettings, error) {
	return func(settings computeruse.RemoteSettings, err error) {
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		envelope.WriteJSON(w, http.StatusOK, settings)
	}
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
