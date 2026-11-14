package controllers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionfork"
)

// SessionForkService forks a session into a new one.
type SessionForkService interface {
	Enabled() bool
	Options(ctx context.Context, id domain.SessionID) (sessionfork.Options, error)
	ForkTurn(ctx context.Context, id domain.SessionID, clientRequestID, turnID string) (domain.SessionRecord, error)
}

// SessionForksController serves the session fork routes.
type SessionForksController struct {
	Svc SessionForkService
}

// Register mounts the fork routes.
func (c *SessionForksController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/fork-options", c.options)
	r.Post("/sessions/{sessionId}/fork", c.fork)
}

func (c *SessionForksController) options(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil || !c.Svc.Enabled() {
		writeForkError(w, r, sessionfork.ErrDisabled)
		return
	}
	opts, err := c.Svc.Options(r.Context(), sessionID(r))
	if err != nil {
		writeForkError(w, r, err)
		return
	}
	out := SessionForkOptionsResponse{Allowed: opts.Allowed, SuggestedName: opts.SuggestedName, AnyTurn: opts.AnyTurn}
	if opts.Refusal != nil {
		out.Refusal = &SessionForkRefusal{Code: opts.Refusal.Code, Message: opts.Refusal.Message}
	}
	if o := opts.ForkedFrom; o != nil {
		out.ForkedFrom = &SessionForkOrigin{SessionID: o.SessionID, Title: o.Title, TurnID: o.TurnID, SourceExists: o.SourceExists, TextHandoff: o.TextHandoff}
	}
	for _, f := range opts.Forks {
		out.Forks = append(out.Forks, SessionForkSummary{SessionID: f.SessionID, Title: f.Title, Exists: f.Exists})
	}
	for _, pr := range opts.SourcePRs {
		out.SourcePRs = append(out.SourcePRs, SessionForkSourcePR{URL: pr.URL, Number: pr.Number, Merged: pr.Merged, Closed: pr.Closed})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *SessionForksController) fork(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil || !c.Svc.Enabled() {
		writeForkError(w, r, sessionfork.ErrDisabled)
		return
	}
	var in ForkSessionRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.ClientRequestID) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "CLIENT_REQUEST_ID_REQUIRED", "clientRequestId is required", nil)
		return
	}
	rec, err := c.Svc.ForkTurn(r.Context(), sessionID(r), in.ClientRequestID, strings.TrimSpace(in.TurnID))
	if err != nil {
		writeForkError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ForkSessionResponse{SessionID: rec.ID, DisplayName: rec.DisplayName})
}

func writeForkError(w http.ResponseWriter, r *http.Request, err error) {
	var refusal *sessionfork.Refusal
	switch {
	case errors.Is(err, sessionfork.ErrDisabled):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_FORK_DISABLED", "Session forks are turned off on this daemon", nil)
	case errors.Is(err, sessionfork.ErrSessionNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Session not found", nil)
	case errors.As(err, &refusal):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", refusal.Code, refusal.Message, nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SESSION_FORK_FAILED", err.Error(), nil)
	}
}
