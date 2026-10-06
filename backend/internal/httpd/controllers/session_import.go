package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
)

// SessionImportService scans only after a user opens import, and registers only selected histories.
type SessionImportService interface {
	Scan(context.Context) (sessionimport.Preview, error)
	Import(context.Context, []string) ([]sessionimport.Result, error)
}

// SessionImportController exposes local history discovery and import.
type SessionImportController struct{ Svc SessionImportService }

// Register mounts history discovery and import routes.
func (c *SessionImportController) Register(r chi.Router) {
	r.Get("/session-import", c.scan)
	r.Post("/session-import", c.importSelected)
}

func (c *SessionImportController) scan(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/session-import")
		return
	}
	preview, err := c.Svc.Scan(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, preview)
}

func (c *SessionImportController) importSelected(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/session-import")
		return
	}
	var in SessionImportRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	results, err := c.Svc.Import(r.Context(), in.IDs)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionImportResponse{Results: results})
}
