package controllers

import (
	"context"
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
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// CodexAccountsManagerService is the Codex-only accounts management boundary.
type CodexAccountsManagerService interface {
	Status() (string, string)
	ManagementSnapshot(context.Context) (accountsmanager.ManagementSnapshot, error)
	AddAPIKey(context.Context, accountsmanager.APIKeyInput) (accountsmanager.ManagementSnapshot, error)
	ImportCredential(context.Context, accountsmanager.CredentialImport) (accountsmanager.ManagementSnapshot, error)
	SetAccountDisabled(context.Context, string, bool) (accountsmanager.ManagementSnapshot, error)
	RefreshAccount(context.Context, string) (accountsmanager.ManagementSnapshot, error)
	RemoveAccount(context.Context, string) (accountsmanager.ManagementSnapshot, error)
	Models(context.Context, string) ([]accountsmanager.AccountModel, error)
	Quota(context.Context, string) (accountsmanager.AccountQuota, error)
	ResetQuota(context.Context, string) error
	SetRoutingPolicy(context.Context, bool, []string) (accountsmanager.ManagementSnapshot, error)
}

// CodexAccountsManagerController exposes the small management protocol used by
// Settings. It deliberately shares AO's existing Codex login terminal rather
// than adding another OAuth implementation beside it.
type CodexAccountsManagerController struct {
	Manager CodexAccountsManagerService
	OAuth   CodexAccountService
}

// Register registers the Codex accounts manager HTTP routes.
func (c *CodexAccountsManagerController) Register(r chi.Router) {
	r.Get("/accounts-manager/status", c.status)
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
	r.Put("/accounts-manager/routing/{provider}", c.routing)
}

// RegisterStreams registers the live management snapshot stream.
func (c *CodexAccountsManagerController) RegisterStreams(r chi.Router) {
	r.Get("/accounts-manager/accounts/events", c.events)
}

func (c *CodexAccountsManagerController) status(w http.ResponseWriter, r *http.Request) {
	if c.Manager == nil {
		c.notImplemented(w, r)
		return
	}
	state, reason := c.Manager.Status()
	response := AccountsManagerStatusResponse{State: state}
	if reason != "" {
		response.Reason = &reason
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

func (c *CodexAccountsManagerController) accounts(w http.ResponseWriter, r *http.Request) {
	snapshot, err := c.snapshot(r)
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, newAccountsManagerResponse(snapshot))
}

func (c *CodexAccountsManagerController) startOAuth(w http.ResponseWriter, r *http.Request) {
	if c.OAuth == nil {
		c.notImplemented(w, r)
		return
	}
	var request StartAccountsManagerOAuthRequest
	if !c.decode(w, r, 16<<10, &request) {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(request.Provider), "codex") {
		c.writeError(w, r, accountsmanager.ErrAccountInvalid)
		return
	}
	started, err := c.OAuth.OpenCodexAccountLoginTerminal(r.Context())
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, AccountsManagerOAuthSessionResponse{
		ID: started.Operation.OperationID, Provider: "codex", Mode: request.Mode,
		Status: string(started.Operation.Status), ExpiresAt: started.Operation.ExpiresAt,
		ShellTerminal: &CodexAccountLoginTerminalResponse{HandleID: started.ShellTerminal.HandleID, Title: started.ShellTerminal.Title, CreatedAt: started.ShellTerminal.CreatedAt},
	})
}

func (c *CodexAccountsManagerController) cancelOAuth(w http.ResponseWriter, r *http.Request) {
	if c.OAuth == nil {
		c.notImplemented(w, r)
		return
	}
	if _, err := c.OAuth.CancelCodexAccountLogin(r.Context(), strings.TrimSpace(chi.URLParam(r, "operationId"))); err != nil {
		c.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *CodexAccountsManagerController) addAPIKey(w http.ResponseWriter, r *http.Request) {
	var request AccountsManagerAPIKeyRequest
	if !c.decode(w, r, 16<<10, &request) {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(request.Provider), "codex") {
		c.writeError(w, r, accountsmanager.ErrAccountInvalid)
		return
	}
	snapshot, err := c.Manager.AddAPIKey(r.Context(), accountsmanager.APIKeyInput{Key: request.Key, Label: request.Label, BaseURL: request.BaseURL})
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, newAccountsManagerResponse(snapshot))
}

func (c *CodexAccountsManagerController) importCredential(w http.ResponseWriter, r *http.Request) {
	var request AccountsManagerImportRequest
	if !c.decode(w, r, (1<<20)+(16<<10), &request) {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(request.Provider), "codex") {
		c.writeError(w, r, accountsmanager.ErrAccountInvalid)
		return
	}
	snapshot, err := c.Manager.ImportCredential(r.Context(), accountsmanager.CredentialImport{Filename: request.Filename, JSON: request.Credential})
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, newAccountsManagerResponse(snapshot))
}

func (c *CodexAccountsManagerController) updateAccount(w http.ResponseWriter, r *http.Request) {
	var request UpdateAccountsManagerAccountRequest
	if !c.decode(w, r, 1024, &request) {
		return
	}
	snapshot, err := c.Manager.SetAccountDisabled(r.Context(), chi.URLParam(r, "accountId"), request.Disabled)
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, newAccountsManagerResponse(snapshot))
}

func (c *CodexAccountsManagerController) refreshAccount(w http.ResponseWriter, r *http.Request) {
	if c.Manager == nil {
		c.notImplemented(w, r)
		return
	}
	snapshot, err := c.Manager.RefreshAccount(r.Context(), chi.URLParam(r, "accountId"))
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, newAccountsManagerResponse(snapshot))
}

func (c *CodexAccountsManagerController) removeAccount(w http.ResponseWriter, r *http.Request) {
	if c.Manager == nil {
		c.notImplemented(w, r)
		return
	}
	snapshot, err := c.Manager.RemoveAccount(r.Context(), chi.URLParam(r, "accountId"))
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, newAccountsManagerResponse(snapshot))
}

func (c *CodexAccountsManagerController) models(w http.ResponseWriter, r *http.Request) {
	if c.Manager == nil {
		c.notImplemented(w, r)
		return
	}
	models, err := c.Manager.Models(r.Context(), chi.URLParam(r, "accountId"))
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	result := make([]AccountsManagerModelResponse, len(models))
	for index, model := range models {
		result[index] = AccountsManagerModelResponse{ID: model.ID}
	}
	envelope.WriteJSON(w, http.StatusOK, AccountsManagerModelsResponse{Models: result})
}

func (c *CodexAccountsManagerController) quota(w http.ResponseWriter, r *http.Request) {
	if c.Manager == nil {
		c.notImplemented(w, r)
		return
	}
	quota, err := c.Manager.Quota(r.Context(), chi.URLParam(r, "accountId"))
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, AccountsManagerQuotaResponse{Exceeded: quota.Exceeded, Reason: quota.Reason, NextRecoverAt: quota.NextRecoverAt, ObservedAt: quota.ObservedAt, Signals: quota.Signals})
}

func (c *CodexAccountsManagerController) resetQuota(w http.ResponseWriter, r *http.Request) {
	if c.Manager == nil {
		c.notImplemented(w, r)
		return
	}
	if err := c.Manager.ResetQuota(r.Context(), chi.URLParam(r, "accountId")); err != nil {
		c.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *CodexAccountsManagerController) routing(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(strings.TrimSpace(chi.URLParam(r, "provider")), "codex") {
		c.writeError(w, r, accountsmanager.ErrAccountInvalid)
		return
	}
	var request UpdateAccountsManagerRoutingRequest
	if !c.decode(w, r, 64<<10, &request) {
		return
	}
	snapshot, err := c.Manager.SetRoutingPolicy(r.Context(), request.Enabled, request.AccountIDs)
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, newAccountsManagerResponse(snapshot))
}

func (c *CodexAccountsManagerController) events(w http.ResponseWriter, r *http.Request) {
	if c.Manager == nil {
		c.notImplemented(w, r)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		c.writeError(w, r, errors.New("stream unavailable"))
		return
	}
	snapshot, err := c.snapshot(r)
	if err != nil {
		c.writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	emit := func(value accountsmanager.ManagementSnapshot) bool {
		data, marshalErr := json.Marshal(newAccountsManagerResponse(value))
		if marshalErr != nil {
			return false
		}
		if _, writeErr := fmt.Fprintf(w, "event: accounts_manager\ndata: %s\n\n", data); writeErr != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !emit(snapshot) {
		return
	}
	source, subscribed := c.Manager.(interface {
		Subscribe(context.Context) <-chan accountsmanager.ManagementSnapshot
	})
	if !subscribed {
		<-r.Context().Done()
		return
	}
	updates := source.Subscribe(r.Context())
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
		case update, open := <-updates:
			if !open || !emit(update) {
				return
			}
		}
	}
}

func (c *CodexAccountsManagerController) snapshot(r *http.Request) (accountsmanager.ManagementSnapshot, error) {
	if c.Manager == nil {
		return accountsmanager.ManagementSnapshot{}, accountsmanager.ErrAccountNotFound
	}
	return c.Manager.ManagementSnapshot(r.Context())
}

func (c *CodexAccountsManagerController) decode(w http.ResponseWriter, r *http.Request, limit int64, out any) bool {
	if c.Manager == nil {
		c.notImplemented(w, r)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_REQUEST", "Invalid Accounts Manager request", nil)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_REQUEST", "Invalid Accounts Manager request", nil)
		return false
	}
	return true
}

func (c *CodexAccountsManagerController) notImplemented(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, r.Method, r.URL.Path)
}

func (c *CodexAccountsManagerController) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ports.ErrCodexProxyUnavailable):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "CODEX_PROXY_UNAVAILABLE", "The Codex accounts manager is unavailable", nil)
	case errors.Is(err, ports.ErrCodexProxyNoAccounts):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "CODEX_PROXY_NO_ACCOUNTS", "No Codex accounts are available", nil)
	case errors.Is(err, ports.ErrCodexProxyAccountUnavailable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "CODEX_PROXY_ACCOUNT_UNAVAILABLE", "The selected Codex account is unavailable", nil)
	case errors.Is(err, accountsmanager.ErrAccountNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ACCOUNTS_MANAGER_ACCOUNT_NOT_FOUND", "Account was not found", nil)
	case errors.Is(err, accountsmanager.ErrAccountInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "ACCOUNTS_MANAGER_INVALID_CREDENTIAL", "Credential is invalid", nil)
	case errors.Is(err, accountsmanager.ErrAccountConflict):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "ROUTING_NOT_CONFIGURED", "Choose at least one available account", nil)
	case errors.Is(err, accountsmanager.ErrAccountNotManaged):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "ACCOUNTS_MANAGER_ACCOUNT_NOT_MANAGED", "This native account is managed by Codex account settings", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}

func newAccountsManagerResponse(snapshot accountsmanager.ManagementSnapshot) AccountsManagerAccountsResponse {
	accounts := make([]AccountsManagerAccountResponse, len(snapshot.Accounts))
	for index, account := range snapshot.Accounts {
		models := make([]AccountsManagerModelResponse, len(account.Models))
		for modelIndex, model := range account.Models {
			models[modelIndex] = AccountsManagerModelResponse{ID: model}
		}
		cooldowns := make([]AccountsManagerCooldownResponse, len(account.Cooldowns))
		for cooldownIndex, cooldown := range account.Cooldowns {
			cooldowns[cooldownIndex] = AccountsManagerCooldownResponse{Model: cooldown.Model, Reason: cooldown.Reason, RetryAt: cooldown.RetryAt, RemainingSeconds: cooldown.RemainingSeconds}
		}
		accounts[index] = AccountsManagerAccountResponse{ID: account.ID, Provider: account.Provider, Kind: account.Kind, Email: account.Email, Status: account.Status, Disabled: account.Disabled, Unavailable: account.Unavailable, CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt, LastRefreshedAt: account.LastRefreshedAt, Models: models, Cooldowns: cooldowns}
	}
	return AccountsManagerAccountsResponse{Revision: snapshot.Revision, Availability: snapshot.Availability, Stale: snapshot.Stale, Accounts: accounts, Routing: AccountsManagerRoutingResponse{Provider: "codex", Enabled: snapshot.Routing.Enabled, AccountIDs: snapshot.Routing.AccountIDs}}
}
