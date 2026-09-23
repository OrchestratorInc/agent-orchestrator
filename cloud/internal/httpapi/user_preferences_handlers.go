package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

type putUserPreferencesRequest struct {
	SandboxProvider json.RawMessage `json:"sandboxProvider"`
	InitializeOnly  bool            `json:"initializeOnly,omitempty"`
}

type userPreferencesResponse struct {
	SandboxProvider *string `json:"sandboxProvider"`
}

func preferenceResponse(provider string) userPreferencesResponse {
	if provider == "" {
		return userPreferencesResponse{}
	}
	return userPreferencesResponse{SandboxProvider: &provider}
}

func (s *Server) getUserPreferences(w http.ResponseWriter, r *http.Request) {
	provider, _, err := s.store.GetUserSandboxProvider(r.Context(), principalFrom(r).UserID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, preferenceResponse(provider))
}

func (s *Server) putUserPreferences(w http.ResponseWriter, r *http.Request) {
	var request putUserPreferencesRequest
	if err := decodeJSON(w, r, &request); err != nil || len(request.SandboxProvider) == 0 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request must include sandboxProvider.")
		return
	}
	var provider string
	if !bytes.Equal(bytes.TrimSpace(request.SandboxProvider), []byte("null")) {
		if err := json.Unmarshal(request.SandboxProvider, &provider); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "sandboxProvider must be a string or null.")
			return
		}
		if strings.TrimSpace(provider) == "" {
			writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "sandboxProvider cannot be empty.")
			return
		}
		if !slices.Contains(s.availableSandboxProviders, provider) {
			writeError(w, r, http.StatusUnprocessableEntity, "provider_unavailable", "The selected sandbox provider is not available on this control plane.")
			return
		}
	}
	stored, err := s.store.PutUserSandboxProvider(r.Context(), principalFrom(r).UserID, provider, request.InitializeOnly)
	if err != nil {
		if request.InitializeOnly && errors.Is(err, postgres.ErrConflict) {
			writeError(w, r, http.StatusConflict, "preference_conflict", "A Cloud provider preference already exists. Reload the current choice.")
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, preferenceResponse(stored))
}
