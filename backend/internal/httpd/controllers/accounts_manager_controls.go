package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

var errAccountControlNotFound = errors.New("account control operation not found")

// AccountsManagerControls is wired only after runtime admission is available.
// Implementations must recheck durable operation ownership on every mutation.
type AccountsManagerControls interface {
	SessionAccount(context.Context, domain.SessionID) (domain.AccountsManagerSessionRoute, *domain.AccountsManagerSwitch, error)
	StartAccountSwitch(context.Context, domain.AccountsManagerSwitch) (domain.AccountsManagerSwitch, error)
	AccountSwitch(context.Context, domain.SessionID, string) (domain.AccountsManagerSwitch, error)
	RetryAccountSwitch(context.Context, domain.SessionID, string) (domain.AccountsManagerSwitch, error)
	CancelAccountSwitch(context.Context, domain.SessionID, string) (domain.AccountsManagerSwitch, error)
	AccountRemovalImpact(context.Context, string) (domain.AccountsManagerRemovalImpact, error)
	StartAccountRemoval(context.Context, string, string, int64, bool) (domain.AccountsManagerRemoval, error)
	AccountRemoval(context.Context, string) (domain.AccountsManagerRemoval, error)
	RetryAccountRemoval(context.Context, string) (domain.AccountsManagerRemoval, error)
	CancelAccountRemoval(context.Context, string) (domain.AccountsManagerRemoval, error)
}

func (c *AccountsManagerController) registerSessionControls(r chi.Router) {
	r.Get("/sessions/{sessionId}/account", c.sessionAccount)
	r.Post("/sessions/{sessionId}/account-switches", c.startSessionAccountSwitch)
	r.Get("/sessions/{sessionId}/account-switches/{operationId}", c.sessionAccountSwitch)
	r.Post("/sessions/{sessionId}/account-switches/{operationId}/retry", c.sessionAccountSwitch)
	r.Post("/sessions/{sessionId}/account-switches/{operationId}/cancel", c.sessionAccountSwitch)
	r.Get("/accounts-manager/accounts/{accountId}/removal-impact", c.accountRemovalImpact)
	r.Post("/accounts-manager/accounts/{accountId}/removals", c.startAccountRemoval)
	r.Get("/accounts-manager/removals/{operationId}", c.accountRemovalOperation)
	r.Post("/accounts-manager/removals/{operationId}/retry", c.accountRemovalOperation)
	r.Post("/accounts-manager/removals/{operationId}/cancel", c.accountRemovalOperation)
}

func (c *AccountsManagerController) controlsAvailable(w http.ResponseWriter, r *http.Request) bool {
	if c.Controls == nil {
		pattern := chi.RouteContext(r.Context()).RoutePattern()
		if !strings.HasPrefix(pattern, "/api/v1/") {
			pattern = "/api/v1" + pattern
		}
		apispec.NotImplemented(w, r, r.Method, pattern)
		return false
	}
	for _, name := range []string{"sessionId", "accountId", "operationId"} {
		if value := chi.URLParam(r, name); value != "" && !validAccountControlID(value) {
			invalidAccountControl(w, r)
			return false
		}
	}
	return true
}

func (c *AccountsManagerController) sessionAccount(w http.ResponseWriter, r *http.Request) {
	if !c.controlsAvailable(w, r) {
		return
	}
	id := domain.SessionID(chi.URLParam(r, "sessionId"))
	binding, pending, err := c.Controls.SessionAccount(r.Context(), id)
	if err != nil {
		c.controlError(w, r, err)
		return
	}
	if binding.SessionID != id || (pending != nil && pending.SessionID != id) {
		c.controlError(w, r, errAccountControlNotFound)
		return
	}
	view := AccountsManagerSessionResponse{SessionID: string(id), Provider: string(binding.Provider), Mode: string(binding.Mode), AccountID: binding.AccountID, Revision: binding.Revision, Blocked: binding.Blocked}
	if pending != nil {
		operation := c.observedAccountSwitchResponse(*pending)
		view.Switch = &operation
	}
	envelope.WriteJSON(w, http.StatusOK, view)
}

func (c *AccountsManagerController) startSessionAccountSwitch(w http.ResponseWriter, r *http.Request) {
	if !c.controlsAvailable(w, r) {
		return
	}
	var input AccountsManagerSwitchRequest
	if !decodeAccountControl(w, r, &input, []string{"operationId", "expectedRevision", "mode", "accountId", "policy", "newConversation"}) {
		return
	}
	mode := domain.AccountsManagerConnectionMode(input.Mode)
	policy := domain.SessionInterfaceTransitionPolicy(input.Policy)
	if !validAccountControlID(input.OperationID) || !validAccountControlRevision(input.ExpectedRevision, false) || !policy.Valid() ||
		(mode != domain.AccountsManagerManaged && mode != domain.AccountsManagerNative) ||
		(mode == domain.AccountsManagerManaged && !validAccountControlID(input.AccountID)) ||
		(mode == domain.AccountsManagerNative && input.AccountID != "") {
		invalidAccountControl(w, r)
		return
	}
	id := domain.SessionID(chi.URLParam(r, "sessionId"))
	op, err := c.Controls.StartAccountSwitch(r.Context(), domain.AccountsManagerSwitch{ID: input.OperationID, SessionID: id, SourceRevision: input.ExpectedRevision, TargetMode: mode, TargetAccountID: input.AccountID, Policy: policy, NewConversation: input.NewConversation})
	if err != nil {
		c.controlError(w, r, err)
		return
	}
	if op.SessionID != id || op.ID != input.OperationID {
		c.controlError(w, r, errAccountControlNotFound)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, c.observedAccountSwitchResponse(op))
}

func (c *AccountsManagerController) sessionAccountSwitch(w http.ResponseWriter, r *http.Request) {
	if !c.controlsAvailable(w, r) {
		return
	}
	id, operationID := domain.SessionID(chi.URLParam(r, "sessionId")), chi.URLParam(r, "operationId")
	op, err := c.Controls.AccountSwitch(r.Context(), id, operationID)
	if err != nil {
		c.controlError(w, r, err)
		return
	}
	if op.SessionID != id || op.ID != operationID {
		c.controlError(w, r, errAccountControlNotFound)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if !emptyAccountControlBody(w, r) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/retry") {
			op, err = c.Controls.RetryAccountSwitch(r.Context(), id, operationID)
		} else {
			op, err = c.Controls.CancelAccountSwitch(r.Context(), id, operationID)
		}
		status = http.StatusAccepted
	}
	if err != nil {
		c.controlError(w, r, err)
		return
	}
	if op.SessionID != id || op.ID != operationID {
		c.controlError(w, r, errAccountControlNotFound)
		return
	}
	envelope.WriteJSON(w, status, c.observedAccountSwitchResponse(op))
}

func (c *AccountsManagerController) accountRemovalImpact(w http.ResponseWriter, r *http.Request) {
	if !c.controlsAvailable(w, r) {
		return
	}
	id := chi.URLParam(r, "accountId")
	impact, err := c.Controls.AccountRemovalImpact(r.Context(), id)
	if err != nil {
		c.controlError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, accountRemovalImpactResponse(id, impact))
}

func (c *AccountsManagerController) startAccountRemoval(w http.ResponseWriter, r *http.Request) {
	if !c.controlsAvailable(w, r) {
		return
	}
	var input AccountsManagerRemovalRequest
	if !decodeAccountControl(w, r, &input, []string{"operationId", "expectedRevision", "confirmed"}, "expectedRevision") {
		return
	}
	if !validAccountControlID(input.OperationID) || !validAccountControlRevision(input.ExpectedRevision, true) || !input.Confirmed {
		invalidAccountControl(w, r)
		return
	}
	id := chi.URLParam(r, "accountId")
	op, err := c.Controls.StartAccountRemoval(r.Context(), input.OperationID, id, input.ExpectedRevision, input.Confirmed)
	if err != nil {
		c.controlError(w, r, err)
		return
	}
	if op.ID != input.OperationID || op.AccountID != id {
		c.controlError(w, r, errAccountControlNotFound)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, accountRemovalResponse(op))
}

func (c *AccountsManagerController) accountRemovalOperation(w http.ResponseWriter, r *http.Request) {
	if !c.controlsAvailable(w, r) {
		return
	}
	id := chi.URLParam(r, "operationId")
	op, err := c.Controls.AccountRemoval(r.Context(), id)
	if err != nil {
		c.controlError(w, r, err)
		return
	}
	if op.ID != id {
		c.controlError(w, r, errAccountControlNotFound)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if !emptyAccountControlBody(w, r) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/retry") {
			op, err = c.Controls.RetryAccountRemoval(r.Context(), id)
		} else {
			op, err = c.Controls.CancelAccountRemoval(r.Context(), id)
		}
		status = http.StatusAccepted
	}
	if err != nil {
		c.controlError(w, r, err)
		return
	}
	if op.ID != id {
		c.controlError(w, r, errAccountControlNotFound)
		return
	}
	envelope.WriteJSON(w, status, accountRemovalResponse(op))
}

func (c *AccountsManagerController) controlError(w http.ResponseWriter, r *http.Request, err error) {
	var typed *apierr.Error
	if errors.As(err, &typed) {
		switch typed.Kind {
		case apierr.KindNotFound:
			err = errAccountControlNotFound
		case apierr.KindNotImplemented:
			envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "NOT_IMPLEMENTED", "Account control is unavailable in this daemon", nil)
			return
		case apierr.KindConflict:
			err = domain.ErrAccountsManagerSwitchConflict
		case apierr.KindInvalid:
			invalidAccountControl(w, r)
			return
		case apierr.KindUnavailable:
			err = accountsmanager.ErrUnavailable
		default:
			envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "INTERNAL_ERROR", "Account control operation could not be completed", nil)
			return
		}
	}
	switch {
	case errors.Is(err, errAccountControlNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ACCOUNTS_MANAGER_CONTROL_NOT_FOUND", "Account control operation was not found", nil)
	case errors.Is(err, domain.ErrAccountsManagerSwitchConflict), errors.Is(err, domain.ErrAccountsManagerBindingConflict), errors.Is(err, domain.ErrAccountsManagerRemovalConflict), errors.Is(err, domain.ErrAccountsManagerAccountDeleting), errors.Is(err, domain.ErrAccountsManagerAccountInUse):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "ACCOUNTS_MANAGER_CONTROL_CONFLICT", "Account or session state changed; refresh before retrying", nil)
	default:
		c.writeError(w, r, err)
	}
}

func validAccountControlID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func validAccountControlRevision(revision int64, zero bool) bool {
	return revision <= 9007199254740991 && (revision > 0 || zero && revision == 0)
}

func invalidAccountControl(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_REQUEST", "Invalid account control request", nil)
}

func decodeAccountControl(w http.ResponseWriter, r *http.Request, out any, allowed []string, required ...string) bool {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			envelope.WriteAPIError(w, r, http.StatusRequestEntityTooLarge, "bad_request", "INPUT_TOO_LARGE", "Account control request is too large", nil)
		} else {
			invalidAccountControl(w, r)
		}
		return false
	}
	check := json.NewDecoder(bytes.NewReader(data))
	opening, err := check.Token()
	if err != nil || opening != json.Delim('{') {
		invalidAccountControl(w, r)
		return false
	}
	seen := make(map[string]bool)
	for check.More() {
		key, keyErr := check.Token()
		name, valid := key.(string)
		var value json.RawMessage
		if keyErr != nil || !valid || !slices.Contains(allowed, name) || seen[name] || check.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			invalidAccountControl(w, r)
			return false
		}
		seen[name] = true
	}
	for _, key := range required {
		if !seen[key] {
			invalidAccountControl(w, r)
			return false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		invalidAccountControl(w, r)
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		invalidAccountControl(w, r)
		return false
	}
	return true
}

func emptyAccountControlBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	return decodeAccountControl(w, r, &struct{}{}, nil)
}

func (c *AccountsManagerController) observedAccountSwitchResponse(op domain.AccountsManagerSwitch) AccountsManagerSwitchResponse {
	view := accountSwitchResponse(op)
	if reader, ok := c.Controls.(interface {
		AccountSwitchCanRetry(domain.AccountsManagerSwitch) bool
	}); ok {
		view.CanRetry = reader.AccountSwitchCanRetry(op)
	}
	return view
}

func accountSwitchResponse(op domain.AccountsManagerSwitch) AccountsManagerSwitchResponse {
	return AccountsManagerSwitchResponse{ID: op.ID, SessionID: string(op.SessionID), Provider: string(op.Provider), SourceMode: string(op.SourceMode), SourceAccountID: op.SourceAccountID, SourceRevision: op.SourceRevision, TargetMode: string(op.TargetMode), TargetAccountID: op.TargetAccountID, TargetRevision: op.TargetRevision, Policy: string(op.Policy), NewConversation: op.NewConversation, Phase: string(op.Phase), ErrorCode: accountControlFailureCode(string(op.Phase), op.ErrorCode), RecoveryRequired: op.Phase == domain.AccountsManagerSwitchRecoveryRequired, CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt}
}

func accountRemovalImpactResponse(id string, impact domain.AccountsManagerRemovalImpact) AccountsManagerRemovalImpactResponse {
	view := AccountsManagerRemovalImpactResponse{AccountID: id, Revision: impact.Revision, Sessions: make([]AccountsManagerRemovalSessionResponse, 0, len(impact.Sessions))}
	for _, session := range impact.Sessions {
		view.Sessions = append(view.Sessions, AccountsManagerRemovalSessionResponse{SessionID: string(session.SessionID), Provider: string(session.Provider), BindingRevision: session.BindingRevision, Stopped: session.Stopped})
	}
	return view
}

func accountRemovalResponse(op domain.AccountsManagerRemoval) AccountsManagerRemovalResponse {
	return AccountsManagerRemovalResponse{ID: op.ID, AccountID: op.AccountID, Impact: accountRemovalImpactResponse(op.AccountID, op.Impact), Phase: string(op.Phase), ErrorCode: accountControlFailureCode(string(op.Phase), op.ErrorCode), CanCancel: !op.StopStarted && !op.Phase.Terminal(), RecoveryRequired: op.Phase == domain.AccountsManagerRemovalRecovery, CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt}
}
