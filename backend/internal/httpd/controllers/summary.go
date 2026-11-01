package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// CardSummaries writes the card summary for a session, holding back updates
// that come too soon after the previous one.
type CardSummaries interface {
	Update(ctx context.Context, id domain.SessionID, summary string, done bool) (found bool, retryAfter time.Duration, err error)
}

// SummaryController owns the /summary route.
type SummaryController struct{ Summaries CardSummaries }

// Register mounts summary routes on r.
func (c *SummaryController) Register(r chi.Router) {
	r.Post("/summary", c.update)
}

// UpdateSummaryRequest is the JSON body for POST /api/v1/summary.
type UpdateSummaryRequest struct {
	SessionID string `json:"sessionId"`
	Summary   string `json:"summary" minLength:"1" maxLength:"200"`
	// Done marks the agent's final summary, which is never held back.
	Done bool `json:"done,omitempty"`
}

// UpdateSummaryResponse is the JSON response for POST /api/v1/summary.
type UpdateSummaryResponse struct {
	OK bool `json:"ok"`
	// Updated is false when the summary was held back because the previous
	// one is too recent.
	Updated bool `json:"updated"`
	// RetryAfterSeconds is how long until a held-back update would be accepted.
	RetryAfterSeconds int `json:"retryAfterSeconds,omitempty"`
}

func (c *SummaryController) update(w http.ResponseWriter, r *http.Request) {
	if c.Summaries == nil {
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
	summary := domain.NormalizeCardSummary(req.Summary)
	if summary == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "SUMMARY_INVALID", fmt.Sprintf("summary must be 1 to %d characters of plain text", domain.MaxCardSummaryRunes), nil)
		return
	}
	found, retryAfter, err := c.Summaries.Update(r.Context(), domain.SessionID(req.SessionID), summary, req.Done)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !found {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Session not found", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, UpdateSummaryResponse{
		OK:                true,
		Updated:           retryAfter == 0,
		RetryAfterSeconds: int(math.Ceil(retryAfter.Seconds())),
	})
}
