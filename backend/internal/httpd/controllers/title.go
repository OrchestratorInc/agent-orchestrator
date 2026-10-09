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

// TitleStore renames a session's display name.
type TitleStore interface {
	RenameSession(ctx context.Context, id domain.SessionID, displayName string, updatedAt time.Time) (bool, error)
}

// TitleController owns the /title route.
type TitleController struct{ Store TitleStore }

// Register mounts title routes on r.
func (c *TitleController) Register(r chi.Router) {
	r.Post("/title", c.update)
}

// UpdateTitleRequest is the JSON body for POST /api/v1/title.
type UpdateTitleRequest struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title" minLength:"1" maxLength:"100"`
}

func (c *TitleController) update(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		envelope.WriteJSON(w, http.StatusNotImplemented, map[string]any{"ok": false})
		return
	}
	var req UpdateTitleRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if req.SessionID == "" || req.Title == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "TITLE_REQUIRED", "sessionId and title are required", nil)
		return
	}
	updated, err := c.Store.RenameSession(r.Context(), domain.SessionID(req.SessionID), req.Title, time.Now().UTC())
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
