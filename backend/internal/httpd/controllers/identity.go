package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

const MobileAPIVersion = 1

type IdentityController struct {
	HostID string
}

func (c *IdentityController) Register(r chi.Router) {
	r.Get("/identity", c.identity)
}

func (c *IdentityController) identity(w http.ResponseWriter, r *http.Request) {
	if c.HostID == "" {
		apispec.NotImplemented(w, r, "GET", "/api/v1/identity")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, IdentityResponse{
		HostID:     c.HostID,
		APIVersion: MobileAPIVersion,
	})
}
