package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// SummaryStore writes the card summary for a session.
type SummaryStore interface {
	UpdateSessionCardSummary(ctx context.Context, id domain.SessionID, summary string, updatedAt time.Time) (bool, error)
}

// SummaryController owns the /summary route.
type SummaryController struct{ Store SummaryStore }

// Register mounts summary routes on r.
func (c *SummaryController) Register(r chi.Router) {
	r.Post("/summary", c.update)
}

// UpdateSummaryRequest is the JSON body for POST /api/v1/summary.
type UpdateSummaryRequest struct {
	SessionID string `json:"sessionId"`
	Summary   string `json:"summary" minLength:"1" maxLength:"200"`
}

// UpdateSummaryResponse is the JSON response for POST /api/v1/summary.
type UpdateSummaryResponse struct {
	OK bool `json:"ok"`
}

func (c *SummaryController) update(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		envelope.WriteJSON(w, http.StatusNotImplemented, map[string]any{"ok": false})
		return
	}
	var req UpdateSummaryRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if req.SessionID == "" || req.Summary == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "SUMMARY_REQUIRED", "sessionId and summary are required", nil)
		return
	}
	value := domain.CardSummaryMetadataPrefix + req.Summary
	updated, err := c.Store.UpdateSessionCardSummary(r.Context(), domain.SessionID(req.SessionID), value, time.Now().UTC())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !updated {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Session not found", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
