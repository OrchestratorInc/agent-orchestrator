package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/go-chi/chi/v5"
)

type sessionPreferencesStore interface {
	AvailableSessionReviewerHarnesses(context.Context, domain.Principal, string, string) ([]string, error)
	UpdateSessionPreferences(context.Context, domain.Principal, string, string, postgres.SessionPreferencesUpdate) (domain.Session, error)
}

type updateSessionPreferencesRequest struct {
	ReviewerHarness    *string `json:"reviewerHarness"`
	AutoReviewEnabled  *bool   `json:"autoReviewEnabled"`
	AutoInjectCI       *bool   `json:"autoInjectCI"`
	AutoInjectReview   *bool   `json:"autoInjectReview"`
	TerminateOnPRMerge *bool   `json:"terminateOnPrMerge"`
}

func (s *Server) updateSessionPreferences(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	store, ok := s.store.(sessionPreferencesStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Cloud session controls are unavailable.")
		return
	}
	var request updateSessionPreferencesRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The session controls request is invalid.")
		return
	}
	if request.ReviewerHarness == nil && request.AutoReviewEnabled == nil && request.AutoInjectCI == nil && request.AutoInjectReview == nil && request.TerminateOnPRMerge == nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "At least one session control is required.")
		return
	}
	session, err := store.UpdateSessionPreferences(r.Context(), principalFrom(r), orgID, sessionID, postgres.SessionPreferencesUpdate{
		ReviewerHarness:    request.ReviewerHarness,
		AutoReviewEnabled:  request.AutoReviewEnabled,
		AutoInjectCI:       request.AutoInjectCI,
		AutoInjectReview:   request.AutoInjectReview,
		TerminateOnPRMerge: request.TerminateOnPRMerge,
	})
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": toSessionResponse(session, nil)})
}

type sessionRenameStore interface {
	RenameSession(context.Context, domain.Principal, string, string, string) (domain.Session, error)
}

type updateSessionRequest struct {
	DisplayName *string `json:"displayName"`
}

// updateSession renames a session, as the local daemon's PATCH /sessions/{id}
// does: 1 to 100 characters, by anyone who may edit the session.
func (s *Server) updateSession(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	store, ok := s.store.(sessionRenameStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Renaming cloud sessions is unavailable.")
		return
	}
	var request updateSessionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The session update request is invalid.")
		return
	}
	// The local daemon's codes and limits for the same request.
	name := ""
	if request.DisplayName != nil {
		name = strings.TrimSpace(*request.DisplayName)
	}
	if name == "" {
		writeError(w, r, http.StatusUnprocessableEntity, "DISPLAY_NAME_REQUIRED", "displayName is required.")
		return
	}
	if utf8.RuneCountInString(name) > 100 {
		writeError(w, r, http.StatusUnprocessableEntity, "DISPLAY_NAME_TOO_LONG", "displayName must be 100 characters or fewer.")
		return
	}
	session, err := store.RenameSession(r.Context(), principalFrom(r), orgID, sessionID, name)
	if errors.Is(err, postgres.ErrOrchestratorRename) {
		writeError(w, r, http.StatusUnprocessableEntity, "ORCHESTRATOR_RENAME_UNSUPPORTED", "The project orchestrator cannot be renamed.")
		return
	}
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": toSessionResponse(session, nil)})
}
