package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/agentconnect"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (s *Server) agentLoginCapabilities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"harnesses": s.agentLogins.Capabilities()})
}

func (s *Server) startAgentLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if len(s.agentLogins.Capabilities()) == 0 {
		s.writeAgentLoginError(w, r, agentconnect.ErrUnavailable)
		return
	}
	var input struct {
		OrgID   string `json:"orgId"`
		Harness string `json:"harness"`
	}
	if decodeJSONLimit(w, r, &input, 4096) != nil {
		writeError(w, r, 400, "invalid_request", "The request body is invalid.")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if uuid.Validate(key) != nil || uuid.Validate(input.OrgID) != nil {
		writeError(w, r, 400, "invalid_request", "An organization UUID and Idempotency-Key UUID are required.")
		return
	}
	login, err := s.agentLogins.Start(r.Context(), principalFrom(r), input.OrgID, input.Harness, key)
	if err != nil {
		s.writeAgentLoginError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"login": login})
}

func agentLoginID(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	id := chi.URLParam(r, "id")
	if uuid.Validate(id) != nil {
		writeError(w, r, 400, "invalid_request", "The login ID is invalid.")
		return "", false
	}
	return id, true
}

func (s *Server) getAgentLogin(w http.ResponseWriter, r *http.Request) {
	id, ok := agentLoginID(w, r)
	if !ok {
		return
	}
	login, err := s.agentLogins.Get(r.Context(), principalFrom(r).UserID, id)
	if err != nil {
		s.writeAgentLoginError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"login": login})
}

func (s *Server) submitAgentLoginCode(w http.ResponseWriter, r *http.Request) {
	id, ok := agentLoginID(w, r)
	if !ok {
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if decodeJSONLimit(w, r, &input, 8<<10) != nil {
		writeError(w, r, 400, "invalid_request", "The request body is invalid.")
		return
	}
	login, err := s.agentLogins.SubmitCode(r.Context(), principalFrom(r).UserID, id, input.Code)
	input.Code = ""
	if err != nil {
		s.writeAgentLoginError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"login": login})
}

func (s *Server) cancelAgentLogin(w http.ResponseWriter, r *http.Request) {
	id, ok := agentLoginID(w, r)
	if !ok {
		return
	}
	if err := s.agentLogins.Cancel(r.Context(), principalFrom(r).UserID, id); err != nil {
		s.writeAgentLoginError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func agentLoginRunnerBearer(w http.ResponseWriter, r *http.Request) (string, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 256 {
		writeError(w, r, 401, "unauthorized", "An attempt bearer is required.")
		return "", false
	}
	return parts[1], true
}

func (s *Server) pollAgentLoginRunner(w http.ResponseWriter, r *http.Request) {
	id, ok := agentLoginID(w, r)
	if !ok {
		return
	}
	token, ok := agentLoginRunnerBearer(w, r)
	if !ok {
		return
	}
	state, err := s.agentLogins.PollRunner(r.Context(), id, token)
	if err != nil {
		s.writeAgentLoginError(w, r, err)
		return
	}
	writeJSON(w, 200, state)
}

type loginCredentialValidator struct{ validator credentialValidator }

func (v loginCredentialValidator) Validate(ctx context.Context, harness, kind string, secret []byte) error {
	if v.validator == nil {
		return agentconnect.ErrProviderUnavailable
	}
	if err := v.validator.Validate(ctx, harness, kind, secret); err != nil {
		if errors.Is(err, errInvalidAgentCredential) {
			return agentconnect.ErrInvalidCredential
		}
		return agentconnect.ErrProviderUnavailable
	}
	return nil
}

func (s *Server) reportAgentLoginRunner(w http.ResponseWriter, r *http.Request) {
	id, ok := agentLoginID(w, r)
	if !ok {
		return
	}
	token, ok := agentLoginRunnerBearer(w, r)
	if !ok {
		return
	}
	var input agentconnect.Update
	if decodeJSONLimit(w, r, &input, 128<<10) != nil {
		writeError(w, r, 400, "invalid_request", "The request body is invalid.")
		return
	}
	err := s.agentLogins.Report(r.Context(), id, token, input, loginCredentialValidator{s.credentialValidator})
	input.Secret = ""
	if err != nil {
		s.writeAgentLoginError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeAgentLoginError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, agentconnect.ErrUnavailable):
		writeError(w, r, 503, "agent_login_unavailable", "Native agent login is unavailable in this Cloud environment.")
	case errors.Is(err, agentconnect.ErrNotFound):
		writeError(w, r, 404, "not_found", "The requested login was not found.")
	case errors.Is(err, agentconnect.ErrConflict):
		writeError(w, r, 409, "agent_login_inactive", "This login is no longer active. Start a new login to try again.")
	case errors.Is(err, agentconnect.ErrInvalid):
		writeError(w, r, 422, "validation_error", "The agent login request is invalid.")
	case errors.Is(err, agentconnect.ErrInvalidCredential):
		writeError(w, r, 422, "invalid_credential", "The coding-agent credential is invalid or expired.")
	case errors.Is(err, agentconnect.ErrProviderUnavailable):
		writeError(w, r, 502, "provider_unavailable", "The credential provider could not be reached.")
	case errors.Is(err, postgres.ErrForbidden), errors.Is(err, postgres.ErrNotFound), errors.Is(err, postgres.ErrInvalid), errors.Is(err, postgres.ErrIdempotencyMismatch), errors.Is(err, postgres.ErrSandboxQuotaExceeded), postgres.IsPlanRefusal(err):
		s.writeStoreError(w, r, err)
	default:
		// Never log validator/provider errors here: they can echo a credential
		// or a pasted authorization code. The request ID remains available.
		s.logger.Error("handle native agent login", "request_id", requestID(r))
		writeError(w, r, 500, "internal_error", "The agent login could not be completed.")
	}
}
