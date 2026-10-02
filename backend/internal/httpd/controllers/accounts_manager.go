package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	accountsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager"
)

// AccountsManagerStatusSource provides redacted health without private endpoint access.
type AccountsManagerStatusSource interface {
	Status() accountsmanager.Status
}

// AccountsManagerController exposes only the safe capability state. Process
// coordinates and authentication material remain daemon-internal.
type AccountsManagerController struct {
	Status   AccountsManagerStatusSource
	Service  *accountsvc.Service
	Controls AccountsManagerControls
}

// Register installs management operations separately from long-lived event streams.
func (c *AccountsManagerController) Register(r chi.Router) {
	r.Get("/accounts-manager/status", c.getStatus)
	r.Get("/accounts-manager/accounts", c.accounts)
	r.Post("/accounts-manager/oauth-sessions", c.startOAuth)
	r.Delete("/accounts-manager/oauth-sessions/{operationId}", c.cancelOAuth)
	r.Post("/accounts-manager/accounts/api-key", c.addAPIKey)
	r.Post("/accounts-manager/accounts/import", c.importCredential)
	r.Patch("/accounts-manager/accounts/{accountId}", c.updateAccount)
	r.Post("/accounts-manager/accounts/{accountId}/refresh", c.refreshAccount)
	r.Delete("/accounts-manager/accounts/{accountId}", c.removeAccount)
	r.Get("/accounts-manager/accounts/{accountId}/models", c.models)
	r.Get("/accounts-manager/accounts/{accountId}/quota", c.quota)
	r.Post("/accounts-manager/accounts/{accountId}/quota/reset", c.resetQuota)
	r.Put("/accounts-manager/routing/{provider}", c.updateRouting)
	c.registerSessionControls(r)
}

// RegisterStreams installs event streams outside ordinary request timeout middleware.
func (c *AccountsManagerController) RegisterStreams(r chi.Router) {
	r.Get("/accounts-manager/accounts/events", c.events)
}

func (c *AccountsManagerController) getStatus(w http.ResponseWriter, r *http.Request) {
	if c.Status == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/accounts-manager/status")
		return
	}
	status := c.Status.Status()
	var reason *string
	if status.Reason != "" {
		value := string(status.Reason)
		reason = &value
	}
	envelope.WriteJSON(w, http.StatusOK, AccountsManagerStatusResponse{
		State:         string(status.State),
		Reason:        reason,
		EngineVersion: status.EngineVersion,
	})
}

func (c *AccountsManagerController) accounts(w http.ResponseWriter, r *http.Request) {
	if c.Service == nil {
		c.notImplemented(w, r)
		return
	}
	snapshot, _ := c.Service.Refresh(r.Context())
	envelope.WriteJSON(w, http.StatusOK, newAccountsManagerResponse(snapshot))
}
func (c *AccountsManagerController) startOAuth(w http.ResponseWriter, r *http.Request) {
	var req StartAccountsManagerOAuthRequest
	if !c.decode(w, r, 16<<10, &req) {
		return
	}
	var session accountsvc.OAuthSession
	var err error
	switch {
	case req.AccountID != "" && req.Generation != 0:
		session, err = c.Service.ReconnectOAuth(r.Context(), accountsmanager.Provider(req.Provider), accountsmanager.OAuthMode(req.Mode), req.AccountID, req.Generation)
	case req.AccountID == "" && req.Generation == 0:
		session, err = c.Service.StartOAuth(r.Context(), accountsmanager.Provider(req.Provider), accountsmanager.OAuthMode(req.Mode))
	default:
		err = accountsmanager.ErrInvalidCredential
	}
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, newOAuthSessionResponse(session))
}
func (c *AccountsManagerController) cancelOAuth(w http.ResponseWriter, r *http.Request) {
	if c.Service == nil {
		c.notImplemented(w, r)
		return
	}
	if err := c.Service.CancelOAuth(r.Context(), strings.TrimSpace(chi.URLParam(r, "operationId"))); err != nil {
		c.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (c *AccountsManagerController) addAPIKey(w http.ResponseWriter, r *http.Request) {
	var req AccountsManagerAPIKeyRequest
	if !c.decode(w, r, 16<<10, &req) {
		return
	}
	snapshot, err := c.Service.AddAPIKey(r.Context(), accountsmanager.APIKeyInput{OperationID: req.OperationID, Provider: accountsmanager.Provider(req.Provider), Key: req.Key, BaseURL: req.BaseURL})
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, newAccountsManagerResponse(snapshot))
}
func (c *AccountsManagerController) importCredential(w http.ResponseWriter, r *http.Request) {
	var req AccountsManagerImportRequest
	if !c.decode(w, r, (1<<20)+(16<<10), &req) {
		return
	}
	snapshot, err := c.Service.ImportCredential(r.Context(), accountsmanager.CredentialImport{OperationID: req.OperationID, Provider: accountsmanager.Provider(req.Provider), Name: req.Filename, JSON: req.Credential})
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, newAccountsManagerResponse(snapshot))
}
func (c *AccountsManagerController) updateAccount(w http.ResponseWriter, r *http.Request) {
	var req UpdateAccountsManagerAccountRequest
	if !c.decode(w, r, 1024, &req) {
		return
	}
	var snapshot accountsvc.Snapshot
	var err error
	switch {
	case req.Label != nil && req.Disabled == nil && req.Generation != 0:
		snapshot, err = c.Service.RenameAccount(r.Context(), chi.URLParam(r, "accountId"), *req.Label, req.Generation)
	case req.Disabled != nil && req.Label == nil && req.Generation == 0:
		snapshot, err = c.Service.SetDisabled(r.Context(), chi.URLParam(r, "accountId"), *req.Disabled)
	default:
		err = accountsmanager.ErrInvalidCredential
	}
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, newAccountsManagerResponse(snapshot))
}
func (c *AccountsManagerController) refreshAccount(w http.ResponseWriter, r *http.Request) {
	if c.Service == nil {
		c.notImplemented(w, r)
		return
	}
	snapshot, err := c.Service.RefreshAccount(r.Context(), chi.URLParam(r, "accountId"))
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, newAccountsManagerResponse(snapshot))
}
func (c *AccountsManagerController) removeAccount(w http.ResponseWriter, r *http.Request) {
	if c.Service == nil {
		c.notImplemented(w, r)
		return
	}
	snapshot, err := c.Service.RemoveAccount(r.Context(), chi.URLParam(r, "accountId"))
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, newAccountsManagerResponse(snapshot))
}
func (c *AccountsManagerController) models(w http.ResponseWriter, r *http.Request) {
	if c.Service == nil {
		c.notImplemented(w, r)
		return
	}
	models, err := c.Service.Models(r.Context(), chi.URLParam(r, "accountId"))
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	result := make([]AccountsManagerModelResponse, 0, len(models))
	for _, m := range models {
		result = append(result, AccountsManagerModelResponse{ID: m.ID, DisplayName: m.DisplayName, Type: m.Type, Owner: m.Owner, Efforts: m.Efforts})
	}
	envelope.WriteJSON(w, http.StatusOK, AccountsManagerModelsResponse{Models: result})
}
func (c *AccountsManagerController) quota(w http.ResponseWriter, r *http.Request) {
	if c.Service == nil {
		c.notImplemented(w, r)
		return
	}
	quota, err := c.Service.Quota(r.Context(), chi.URLParam(r, "accountId"))
	if err != nil {
		c.writeQuotaError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, AccountsManagerQuotaResponse{ObservedAt: quota.ObservedAt, Subscription: quota.Subscription, Summary: quota.Summary, ServerTimeOffsetMS: quota.ServerTimeOffsetMS, Groups: quota.Groups})
}

func (c *AccountsManagerController) writeQuotaError(w http.ResponseWriter, r *http.Request, err error) {
	var quotaErr *accountsmanager.QuotaError
	if !errors.As(err, &quotaErr) {
		c.writeError(w, r, err)
		return
	}
	status, code, message := http.StatusServiceUnavailable, "ACCOUNTS_MANAGER_USAGE_UNAVAILABLE", "The usage service is temporarily unavailable"
	switch quotaErr.StatusCode {
	case http.StatusUnauthorized:
		status, code, message = http.StatusUnauthorized, "ACCOUNTS_MANAGER_USAGE_AUTHENTICATION_REQUIRED", "The provider could not authenticate the usage request"
	case http.StatusForbidden:
		status, code, message = http.StatusForbidden, "ACCOUNTS_MANAGER_USAGE_ACCESS_DENIED", "The provider refused the usage request"
	case http.StatusTooManyRequests:
		status, code, message = http.StatusTooManyRequests, "ACCOUNTS_MANAGER_USAGE_RATE_LIMITED", "Usage checks are temporarily rate-limited"
	case http.StatusBadGateway:
		status, code, message = http.StatusBadGateway, "ACCOUNTS_MANAGER_USAGE_RESPONSE_INVALID", "The usage service returned an unreadable response"
	}
	envelope.WriteAPIError(w, r, status, "upstream", code, message, nil)
}

func (c *AccountsManagerController) resetQuota(w http.ResponseWriter, r *http.Request) {
	if c.Service == nil {
		c.notImplemented(w, r)
		return
	}
	if err := c.Service.ResetQuota(r.Context(), chi.URLParam(r, "accountId")); err != nil {
		c.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *AccountsManagerController) updateRouting(w http.ResponseWriter, r *http.Request) {
	var req UpdateAccountsManagerRoutingRequest
	if !c.decode(w, r, 64<<10, &req) {
		return
	}
	snapshot, err := c.Service.SetRoutingPolicy(
		r.Context(),
		accountsmanager.Provider(strings.TrimSpace(chi.URLParam(r, "provider"))),
		req.Enabled,
		req.AccountIDs,
	)
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, newAccountsManagerResponse(snapshot))
}

func (c *AccountsManagerController) events(w http.ResponseWriter, r *http.Request) {
	if c.Service == nil {
		c.notImplemented(w, r)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		envelope.WriteAPIError(w, r, 500, "internal", "SSE_UNSUPPORTED", "Streaming is unavailable", nil)
		return
	}
	updates := c.Service.Subscribe(r.Context())
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case snapshot, open := <-updates:
			if !open {
				return
			}
			data, err := json.Marshal(newAccountsManagerResponse(snapshot))
			if err != nil {
				return
			}
			if _, err = fmt.Fprintf(w, "event: accounts_manager\ndata: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
func (c *AccountsManagerController) decode(w http.ResponseWriter, r *http.Request, limit int64, out any) bool {
	if c.Service == nil {
		c.notImplemented(w, r)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		code := "INVALID_REQUEST"
		status := http.StatusBadRequest
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			code = "INPUT_TOO_LARGE"
			status = http.StatusRequestEntityTooLarge
		}
		envelope.WriteAPIError(w, r, status, "bad_request", code, "Invalid Accounts Manager request", nil)
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		envelope.WriteAPIError(w, r, 400, "bad_request", "INVALID_REQUEST", "Invalid Accounts Manager request", nil)
		return false
	}
	return true
}
func (c *AccountsManagerController) notImplemented(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, r.Method, r.URL.Path)
}
func (c *AccountsManagerController) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, accountsmanager.ErrUnavailable):
		envelope.WriteAPIError(w, r, 503, "unavailable", "ACCOUNTS_MANAGER_UNAVAILABLE", "Accounts Manager is unavailable", nil)
	case errors.Is(err, accountsvc.ErrRoutingNotConfigured):
		envelope.WriteAPIError(w, r, 409, "conflict", "ROUTING_NOT_CONFIGURED", "Choose exactly one available default account before enabling routing", nil)
	case errors.Is(err, accountsvc.ErrRoutingAccountUnavailable):
		envelope.WriteAPIError(w, r, 409, "conflict", "ROUTING_ACCOUNT_UNAVAILABLE", "The account selected for this session is unavailable", nil)
	case errors.Is(err, accountsvc.ErrRoutingNoEligibleAccount):
		envelope.WriteAPIError(w, r, 409, "conflict", "ROUTING_NO_ELIGIBLE_ACCOUNT", "No eligible account is available for routing", nil)
	case errors.Is(err, accountsmanager.ErrUnsupportedProvider):
		envelope.WriteAPIError(w, r, 400, "validation", "ACCOUNTS_MANAGER_PROVIDER_UNSUPPORTED", "Provider must be codex or claude", nil)
	case errors.Is(err, accountsmanager.ErrInvalidCredential):
		envelope.WriteAPIError(w, r, 400, "validation", "ACCOUNTS_MANAGER_INVALID_CREDENTIAL", "Credential is invalid", nil)
	case errors.Is(err, accountsmanager.ErrCredentialMethod):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "ACCOUNTS_MANAGER_CREDENTIAL_METHOD_UNSUPPORTED", "Sign-in tokens cannot be added as API keys. Use the provider-owned native sign-in flow", nil)
	case errors.Is(err, accountsmanager.ErrVerificationUnavailable):
		envelope.WriteAPIError(w, r, 503, "unavailable", "ACCOUNTS_MANAGER_VERIFICATION_UNAVAILABLE", "Could not verify this credential. Check connectivity and credential permissions, then retry", nil)
	case errors.Is(err, accountsmanager.ErrCredentialNotFound):
		envelope.WriteAPIError(w, r, 404, "not_found", "ACCOUNTS_MANAGER_ACCOUNT_NOT_FOUND", "Account was not found", nil)
	case errors.Is(err, accountsmanager.ErrCredentialConflict), errors.Is(err, accountsmanager.ErrOAuthBusy):
		envelope.WriteAPIError(w, r, 409, "conflict", "ACCOUNTS_MANAGER_CONFLICT", "Accounts Manager operation conflicts with existing state", nil)
	case errors.Is(err, accountsmanager.ErrOperationUnsupported):
		envelope.WriteAPIError(w, r, 422, "unsupported", "ACCOUNTS_MANAGER_OPERATION_UNSUPPORTED", "Operation is unsupported for this account", nil)
	case errors.Is(err, accountsmanager.ErrOAuthExpired):
		envelope.WriteAPIError(w, r, 410, "expired", "ACCOUNTS_MANAGER_OAUTH_EXPIRED", "Sign-in session expired", nil)
	case errors.Is(err, accountsmanager.ErrRequestTooLarge):
		envelope.WriteAPIError(w, r, 413, "bad_request", "ACCOUNTS_MANAGER_INPUT_TOO_LARGE", "Input is too large", nil)
	default:
		envelope.WriteAPIError(w, r, 502, "upstream", "ACCOUNTS_MANAGER_OPERATION_FAILED", "Accounts Manager operation failed", nil)
	}
}

func newAccountsManagerResponse(snapshot accountsvc.Snapshot) AccountsManagerAccountsResponse {
	result := AccountsManagerAccountsResponse{Revision: snapshot.Revision, Availability: string(snapshot.Availability), Stale: snapshot.Stale, Accounts: make([]AccountsManagerAccountResponse, 0, len(snapshot.Accounts)), OAuthSessions: make([]AccountsManagerOAuthSessionResponse, 0, len(snapshot.OAuthSessions)), Routing: make([]AccountsManagerRoutingResponse, 0, len(snapshot.Routing))}
	for _, a := range snapshot.Accounts {
		cooldowns := make([]AccountsManagerCooldownResponse, 0, len(a.Cooldowns))
		for _, v := range a.Cooldowns {
			cooldowns = append(cooldowns, AccountsManagerCooldownResponse{Scope: v.Scope, Model: v.Model, Reason: v.Reason, RetryAt: v.RetryAt, RemainingSeconds: v.RemainingSeconds, HTTPStatus: v.HTTPStatus})
		}
		result.Accounts = append(result.Accounts, AccountsManagerAccountResponse{ID: a.ID, Label: a.Label, Generation: a.Generation, ReconnectSupported: a.ReconnectSupported, Verification: a.Verification, VerifiedAt: a.VerifiedAt, Provider: string(a.Provider), Kind: string(a.Kind), Email: a.Email, Status: string(a.Status), Disabled: a.Disabled, Unavailable: a.Unavailable, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, LastRefreshedAt: a.LastRefreshedAt, QuotaSupported: a.QuotaSupported, Cooldowns: cooldowns})
	}
	for _, session := range snapshot.OAuthSessions {
		result.OAuthSessions = append(result.OAuthSessions, newOAuthSessionResponse(session))
	}
	for _, policy := range snapshot.Routing {
		result.Routing = append(result.Routing, AccountsManagerRoutingResponse{Provider: string(policy.Provider), Enabled: policy.Enabled, AccountIDs: append([]string{}, policy.AccountIDs...)})
	}
	return result
}
func newOAuthSessionResponse(s accountsvc.OAuthSession) AccountsManagerOAuthSessionResponse {
	return AccountsManagerOAuthSessionResponse{ID: s.ID, AccountID: s.AccountID, Provider: string(s.Provider), Mode: string(s.Mode), Status: string(s.Status), FailureCode: s.FailureCode, AuthorizationURL: s.AuthorizationURL, UserCode: s.UserCode, ExpiresAt: s.ExpiresAt}
}
