package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TelemetryIdentityController lets a paired phone adopt the desktop's telemetry
// identity and opt-out. Like EndpointsController it sits outside /api/v1/mobile
// (404 on the LAN socket) and is authenticated: the identity names a person.
type TelemetryIdentityController struct {
	Source ports.TelemetryIdentityStore
}

// Register mounts the telemetry identity route on the supplied router.
func (c *TelemetryIdentityController) Register(r chi.Router) {
	r.Get("/telemetry/identity", c.get)
}

func (c *TelemetryIdentityController) get(w http.ResponseWriter, r *http.Request) {
	if c.Source == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/telemetry/identity")
		return
	}
	snap := c.Source.Snapshot()
	resp := TelemetryIdentityResponse{OptedOut: snap.OptedOut}
	if !snap.OptedOut {
		resp.DistinctID = snap.InstallID
		if snap.CloudUserID != "" {
			resp.DistinctID = snap.CloudUserID
		}
		resp.CloudUserID, resp.GitHubLogin = snap.CloudUserID, snap.GitHubLogin
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}
