package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/google/uuid"
)

const maxRequestBody = 1 << 20

type errorEnvelope struct {
	Error     string         `json:"error"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"requestId"`
	Details   map[string]any `json:"details,omitempty"`
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	return decodeJSONLimit(w, r, target, maxRequestBody)
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	return decodeJSONBody(w, r, target, limit, true)
}

// decodeJSONTolerant decodes like decodeJSON but ignores unknown fields, for
// worker requests a newer worker may extend before this control plane knows
// the field (a control-plane rollback).
func decodeJSONTolerant(w http.ResponseWriter, r *http.Request, target any) error {
	return decodeJSONBody(w, r, target, maxRequestBody, false)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, target any, limit int64, strict bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{
		Error:     http.StatusText(status),
		Code:      code,
		Message:   message,
		RequestID: requestID(r),
	})
}

func (s *Server) writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	var refusedBranch *postgres.BranchRefusedError
	s.recordPlanRefusal(r, err)
	if writePlanError(w, r, err) {
		return
	}
	switch {
	case errors.Is(err, postgres.ErrGitHubUserScopeNeedsPersonalOrg):
		writeError(w, r, http.StatusConflict, "github_user_scope_personal_org_only",
			"You don't administer this GitHub installation, so it can only be connected to your personal AO account, where you are the only member.")
	case errors.Is(err, postgres.ErrNotOrchestratorChild):
		writeError(w, r, http.StatusNotFound, "not_orchestrator_child",
			"That session is not one this orchestrator started. An orchestrator can only kill or message the sessions it spawned.")
	case errors.Is(err, postgres.ErrMergeNeedsApproval):
		writeError(w, r, http.StatusForbidden, "MERGE_NEEDS_APPROVAL",
			"Only the project's owner can merge this pull request. Ask them to merge it.")
	case errors.Is(err, postgres.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "forbidden", "You do not have access to this organization.")
	case errors.Is(err, postgres.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "The requested resource was not found.")
	case errors.Is(err, postgres.ErrSessionTerminated):
		writeError(w, r, http.StatusConflict, "SESSION_TERMINATED", "This session was deleted. Restore it to continue.")
	case errors.Is(err, postgres.ErrConflict):
		writeError(w, r, http.StatusConflict, "conflict", "The resource conflicts with an existing record.")
	case errors.Is(err, postgres.ErrComposerLocked):
		writeError(w, r, http.StatusConflict, "COMPOSER_LOCKED",
			"Someone else is writing to the agent right now. Request a turn to go next.")
	case errors.Is(err, postgres.ErrComposerLeaseLost):
		writeError(w, r, http.StatusConflict, "COMPOSER_LEASE_LOST",
			"Your turn to write to the agent ended. Request a turn to write again.")
	case errors.Is(err, postgres.ErrTurnFinished):
		writeError(w, r, http.StatusConflict, "TURN_FINISHED", "The turn is already finished.")
	case errors.Is(err, postgres.ErrStaleTurn):
		writeError(w, r, http.StatusConflict, "STALE_TURN", "The turn is owned by another worker attempt.")
	case errors.Is(err, postgres.ErrInvalid):
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "The request violates a resource constraint.")
	case errors.As(err, &refusedBranch):
		writeBranchRefusal(w, r, refusedBranch)
	case errors.Is(err, postgres.ErrInvalidBranch):
		writeError(
			w, r, http.StatusUnprocessableEntity, "invalid_branch",
			"The branch name is invalid, or it is the project's default branch or session prefix.",
		)
	case errors.Is(err, postgres.ErrIdempotencyMismatch):
		writeError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "The idempotency key was already used for another request.")
	case errors.Is(err, postgres.ErrSandboxQuotaExceeded):
		writeError(
			w, r, http.StatusConflict, "SANDBOX_QUOTA_EXCEEDED",
			"This organization has reached its limit of concurrent sessions. Delete a session and try again.",
		)
	default:
		s.logger.Error("handle API request", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The request could not be completed.")
	}
}

func parseLimit(r *http.Request) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get("limit"))
	if value == "" {
		return 50, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 100 {
		return 0, errors.New("limit must be between 1 and 100")
	}
	return limit, nil
}

func parseCursor(value string) (*domain.Cursor, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New("invalid cursor")
	}
	var encoded struct {
		Time time.Time `json:"time"`
		ID   string    `json:"id"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, errors.New("invalid cursor")
	}
	if encoded.Time.IsZero() || uuid.Validate(encoded.ID) != nil {
		return nil, errors.New("invalid cursor")
	}
	return &domain.Cursor{Time: encoded.Time, ID: encoded.ID}, nil
}

func encodeCursor(value time.Time, id string) string {
	raw, _ := json.Marshal(struct {
		Time time.Time `json:"time"`
		ID   string    `json:"id"`
	}{Time: value, ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func requireUUID(value, field string) error {
	if uuid.Validate(value) != nil {
		return fmt.Errorf("%s must be a UUID", field)
	}
	return nil
}

func idempotencyKey(r *http.Request) (string, error) {
	value := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if value == "" || len(value) > 200 {
		return "", errors.New("Idempotency-Key must be between 1 and 200 characters")
	}
	return value, nil
}

// writeBranchRefusal answers a refused requested session branch with the
// reason: invalid_branch (422) for a name the rules refuse, branch_in_use (409)
// when a live session already works on it. Details carry the reason and, for
// branch_in_use, the session holding the branch.
func writeBranchRefusal(w http.ResponseWriter, r *http.Request, refused *postgres.BranchRefusedError) {
	status, code := http.StatusUnprocessableEntity, "invalid_branch"
	details := map[string]any{"reason": refused.Reason, "branch": refused.Branch}
	if refused.Reason == postgres.BranchReasonInUse {
		status, code = http.StatusConflict, "branch_in_use"
		details["sessionId"] = refused.SessionID
	}
	if refused.Repository != "" {
		details["repository"] = refused.Repository
	}
	writeJSON(w, status, errorEnvelope{
		Error:     http.StatusText(status),
		Code:      code,
		Message:   refused.Message(),
		RequestID: requestID(r),
		Details:   details,
	})
}
