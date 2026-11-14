package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type TelemetryIdentityController struct {
	Source           ports.TelemetryIdentityStore
	LifetimeSessions func(context.Context) (int64, error)
}

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
		resp.DistinctID = snap.DistinctID()
		resp.InstallID = snap.InstallID
		resp.CloudUserID, resp.GitHubLogin = snap.CloudUserID, snap.GitHubLogin
		if snap.CloudUserKnown {
			signedIn := snap.CloudUserID != ""
			resp.IsSignedIn = &signedIn
		}
		if c.LifetimeSessions != nil {
			if count, err := c.LifetimeSessions(r.Context()); err == nil {
				resp.LifetimeSessions = &count
			}
		}
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}
