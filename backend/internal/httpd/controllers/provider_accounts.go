package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ProviderAccountService defines the account operations available to HTTP handlers.
type ProviderAccountService interface {
	State(context.Context) (domain.ProviderAccountState, error)
	SetPrimary(context.Context, string) error
	Remove(context.Context, string, string, bool) error
	Switch(context.Context, domain.SessionID, string) error
	SessionAccount(context.Context, domain.SessionID) (domain.ProviderSessionRoute, bool, error)
	RecoveryRequired(context.Context) (bool, error)
}

type providerPrimaryOptions interface {
	SetPrimaryWithOptions(context.Context, string, bool) error
}

type providerAccountRenamer interface {
	Rename(context.Context, string, string) error
}

type providerAccountUsageReader interface {
	AccountUsages(context.Context, []domain.ProviderAccount) map[string]domain.ProviderAccountUsage
}

type providerAccountSignInReader interface {
	AccountSignInFailures(context.Context, []domain.ProviderAccount, bool) map[string]bool
}

type providerAccountActions interface {
	UseAccountReset(context.Context, string) (string, error)
	ResumeAccount(context.Context, string) error
	RefreshAccountSignIn(context.Context, string) error
}

type providerNativeRefresher interface {
	RefreshNativeAccounts(context.Context) error
	RefreshNativeAccountsIfDue(context.Context) error
}

// ProviderLoginService defines login operations available to HTTP handlers.
type ProviderLoginService interface {
	Start(context.Context, string, string) (ports.ProviderLogin, error)
	Status(context.Context, string) (ports.ProviderLogin, error)
	Cancel(context.Context, string) error
}

// ProviderAccountsController exposes safe account and session routing operations.
type ProviderAccountsController struct {
	Svc   ProviderAccountService
	Login ProviderLoginService
}

// Register registers account management and session assignment routes.
func (c *ProviderAccountsController) Register(r chi.Router) {
	r.Get("/provider-accounts", c.list)
	r.Post("/provider-accounts/login", c.startLogin)
	r.Get("/provider-accounts/login/{loginId}", c.loginStatus)
	r.Delete("/provider-accounts/login/{loginId}", c.cancelLogin)
	r.Put("/provider-accounts/{accountId}/primary", c.setPrimary)
	r.Patch("/provider-accounts/{accountId}", c.rename)
	r.Post("/provider-accounts/{accountId}/sign-out", c.signOut)
	r.Post("/provider-accounts/{accountId}/reset", c.useReset)
	r.Post("/provider-accounts/{accountId}/resume", c.resume)
	r.Post("/provider-accounts/{accountId}/refresh-sign-in", c.refreshSignIn)
	r.Delete("/provider-accounts/{accountId}", c.remove)
	r.Get("/sessions/{sessionId}/provider-account", c.sessionAccount)
	r.Put("/sessions/{sessionId}/provider-account", c.switchAccount)
}
func accountAPIError(err error) error {
	switch {
	case errors.Is(err, ports.ErrProviderLoginCallbackBusy):
		return apierr.Conflict("PROVIDER_LOGIN_CALLBACK_BUSY", ports.ErrProviderLoginCallbackBusy.Error(), nil)
	case errors.Is(err, ports.ErrProviderLoginUnknown):
		return apierr.NotFound("PROVIDER_LOGIN_NOT_FOUND", ports.ErrProviderLoginUnknown.Error())
	case errors.Is(err, ports.ErrProviderAccountConflict):
		return apierr.Conflict("PROVIDER_ACCOUNT_CONFLICT", ports.ErrProviderAccountConflict.Error(), nil)
	case errors.Is(err, ports.ErrProviderAccountBusy):
		return apierr.Conflict("PROVIDER_ACCOUNT_IN_USE", ports.ErrProviderAccountBusy.Error(), nil)
	case errors.Is(err, ports.ErrProviderPrimaryRequired):
		return apierr.Conflict("PROVIDER_PRIMARY_REQUIRED", ports.ErrProviderPrimaryRequired.Error(), nil)
	case errors.Is(err, ports.ErrProviderAccountUnknown):
		return apierr.NotFound("PROVIDER_ACCOUNT_NOT_FOUND", ports.ErrProviderAccountUnknown.Error())
	case errors.Is(err, ports.ErrProviderAccountNameInvalid):
		return apierr.Invalid("PROVIDER_ACCOUNT_NAME_INVALID", ports.ErrProviderAccountNameInvalid.Error(), nil)
	case errors.Is(err, ports.ErrProviderLoginRequired):
		return apierr.Conflict("PROVIDER_LOGIN_REQUIRED", ports.ErrProviderLoginRequired.Error(), nil)
	case errors.Is(err, ports.ErrProviderAccountIncompatible):
		return apierr.Invalid("PROVIDER_ACCOUNT_INCOMPATIBLE", ports.ErrProviderAccountIncompatible.Error(), nil)
	case errors.Is(err, ports.ErrProviderAccountActionUnavailable):
		return apierr.Conflict("PROVIDER_ACCOUNT_ACTION_UNAVAILABLE", ports.ErrProviderAccountActionUnavailable.Error(), nil)
	case errors.Is(err, ports.ErrProviderAccountRecovery):
		return apierr.Conflict("PROVIDER_ACCOUNT_RECOVERY_REQUIRED", ports.ErrProviderAccountRecovery.Error(), nil)
	}
	return err
}
func (c *ProviderAccountsController) ready(w http.ResponseWriter, r *http.Request) bool {
	if c.Svc == nil {
		envelope.WriteAPIError(w, r, 503, "service_unavailable", "PROVIDER_ACCOUNTS_UNAVAILABLE", "Account manager is unavailable in this build", nil)
		return false
	}
	return true
}
func (c *ProviderAccountsController) list(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	// refresh=true is sent when settings opens or the window regains focus:
	// re-read native logins and CLIProxy's sign-in verdict now. Usage keeps its
	// own cache window.
	refresh := r.URL.Query().Get("refresh") == "true"
	if refresher, ok := c.Svc.(providerNativeRefresher); ok {
		// Inventory remains readable during a locked keychain, provider outage,
		// or a pending routing mutation. RecoveryRequired below exposes the latter.
		if refresh {
			_ = refresher.RefreshNativeAccounts(r.Context())
		} else {
			_ = refresher.RefreshNativeAccountsIfDue(r.Context())
		}
	}
	state, err := c.Svc.State(r.Context())
	if err != nil {
		envelope.WriteError(w, r, accountAPIError(err))
		return
	}
	result := ProviderAccountsResponse{Accounts: []ProviderAccountView{}, Defaults: []ProviderPrimaryView{}}
	pending, err := c.Svc.RecoveryRequired(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	result.RecoveryRequired = pending
	primaries := map[string]string{}
	for _, p := range state.Primaries {
		primaries[p.Provider] = p.PrimaryID
	}
	usageByAccount := map[string]domain.ProviderAccountUsage{}
	includeUsage := r.URL.Query().Get("includeUsage") != "false"
	if includeUsage {
		if reader, ok := c.Svc.(providerAccountUsageReader); ok {
			usageByAccount = reader.AccountUsages(r.Context(), state.Accounts)
		}
	}
	// Read after usage: a rejected usage request makes CLIProxy re-check that
	// account's sign-in, and its verdict should be visible in this response.
	signInFailures := map[string]bool{}
	if reader, ok := c.Svc.(providerAccountSignInReader); ok {
		signInFailures = reader.AccountSignInFailures(r.Context(), state.Accounts, refresh)
	}
	for _, provider := range []string{"codex", "claude"} {
		result.Defaults = append(result.Defaults, ProviderPrimaryView{Provider: provider, PrimaryID: primaries[provider], Managed: true})
	}
	for _, a := range state.Accounts {
		displayName := a.DisplayName
		if displayName == "" {
			displayName = domain.GeneratedProviderAccountName(a.Provider, a.ID)
		}
		view := ProviderAccountView{ID: a.ID, Provider: a.Provider, DisplayName: displayName, Email: a.Email, Kind: providerAccountKind(a), Global: a.Global, SignedIn: a.CredentialRef != "", SignInRequired: signInFailures[a.ID], Primary: primaries[a.Provider] == a.ID, Sessions: []string{}}
		if usage, ok := usageByAccount[a.ID]; ok {
			view.Usage = providerAccountUsageView(usage)
		}
		for _, route := range state.Routes {
			if route.AccountID == a.ID {
				view.Sessions = append(view.Sessions, string(route.SessionID))
			}
		}
		result.Accounts = append(result.Accounts, view)
	}
	envelope.WriteJSON(w, 200, result)
}

func (c *ProviderAccountsController) rename(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	service, ok := c.Svc.(providerAccountRenamer)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "service_unavailable", "PROVIDER_ACCOUNTS_UNAVAILABLE", "Account renaming is unavailable in this build", nil)
		return
	}
	var input ProviderAccountNameRequest
	if err := decodeAccountJSON(r, &input); err != nil {
		envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Invalid account name request", nil))
		return
	}
	if err := service.Rename(r.Context(), chi.URLParam(r, "accountId"), input.DisplayName); err != nil {
		envelope.WriteError(w, r, accountAPIError(err))
		return
	}
	c.list(w, r)
}

func providerAccountUsageView(usage domain.ProviderAccountUsage) *ProviderAccountUsageView {
	view := &ProviderAccountUsageView{
		Status: usage.Status, Plan: usage.Plan, PlanTier: usage.PlanTier, ResetCredits: usage.ResetCredits,
		ResetUsable: usage.ResetUsable, ResetBlockedUntil: usage.ResetBlockedUntil,
		RenewsAt: usage.RenewsAt, Organization: usage.Organization, AddedAt: usage.AddedAt, RefreshedAt: usage.RefreshedAt,
		PausedUntil: usage.PausedUntil, PausedReason: usage.PausedReason, SignInEnding: usage.SignInEnding, SignInEndsAt: usage.SignInEndsAt,
		CheckedAt: usage.CheckedAt, Message: usage.Message,
	}
	if usage.Windows != nil {
		view.Windows = make([]ProviderAccountUsageWindowView, 0, len(usage.Windows))
		for _, window := range usage.Windows {
			view.Windows = append(view.Windows, ProviderAccountUsageWindowView{Name: window.Name, Scope: window.Scope, DurationSeconds: window.DurationSeconds, RemainingFraction: window.RemainingFraction, ResetTime: window.ResetTime})
		}
	}
	for _, reset := range usage.Resets {
		view.Resets = append(view.Resets, ProviderAccountResetView{Label: reset.Label, Left: reset.Left, Total: reset.Total, ExpiresAt: reset.ExpiresAt})
	}
	if usage.Credits != nil {
		view.Credits = &ProviderAccountCreditsView{Balance: usage.Credits.Balance, Unlimited: usage.Credits.Unlimited}
	}
	if usage.ExtraUsage != nil && usage.ExtraUsage.Enabled {
		view.ExtraUsage = &ProviderAccountExtraUsageView{UsedCents: usage.ExtraUsage.UsedCents, LimitCents: usage.ExtraUsage.LimitCents}
	}
	for _, bucket := range usage.Requests {
		view.Requests = append(view.Requests, ProviderAccountRequestsView{Succeeded: bucket.Succeeded, Failed: bucket.Failed})
	}
	if tokens := usage.Tokens; tokens != nil {
		view.Tokens = &ProviderAccountTokensView{LatestDay: tokens.LatestDay, LatestDayTokens: tokens.LatestDayTokens, Lifetime: tokens.Lifetime, PeakDaily: tokens.PeakDaily, LongestTurnSeconds: tokens.LongestTurnSeconds, CurrentStreakDays: tokens.CurrentStreakDays, LongestStreakDays: tokens.LongestStreakDays}
	}
	return view
}

// accountAction resolves the service's optional account actions.
func (c *ProviderAccountsController) accountAction(w http.ResponseWriter, r *http.Request) (providerAccountActions, bool) {
	if !c.ready(w, r) {
		return nil, false
	}
	actions, ok := c.Svc.(providerAccountActions)
	if !ok {
		envelope.WriteError(w, r, accountAPIError(ports.ErrProviderAccountActionUnavailable))
	}
	return actions, ok
}

func (c *ProviderAccountsController) useReset(w http.ResponseWriter, r *http.Request) {
	actions, ok := c.accountAction(w, r)
	if !ok {
		return
	}
	outcome, err := actions.UseAccountReset(r.Context(), chi.URLParam(r, "accountId"))
	if err != nil {
		envelope.WriteError(w, r, accountAPIError(err))
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ProviderAccountResetResponse{Outcome: outcome})
}

func (c *ProviderAccountsController) resume(w http.ResponseWriter, r *http.Request) {
	actions, ok := c.accountAction(w, r)
	if !ok {
		return
	}
	if err := actions.ResumeAccount(r.Context(), chi.URLParam(r, "accountId")); err != nil {
		envelope.WriteError(w, r, accountAPIError(err))
		return
	}
	c.list(w, r)
}

func (c *ProviderAccountsController) refreshSignIn(w http.ResponseWriter, r *http.Request) {
	actions, ok := c.accountAction(w, r)
	if !ok {
		return
	}
	if err := actions.RefreshAccountSignIn(r.Context(), chi.URLParam(r, "accountId")); err != nil {
		envelope.WriteError(w, r, accountAPIError(err))
		return
	}
	c.list(w, r)
}

func (c *ProviderAccountsController) setPrimary(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	var input ProviderAccountChangeRequest
	if r.ContentLength != 0 {
		if err := decodeAccountJSON(r, &input); err != nil {
			envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Invalid account change request", nil))
			return
		}
	}
	var err error
	if input.MoveExisting != nil {
		if setter, ok := c.Svc.(providerPrimaryOptions); ok {
			err = setter.SetPrimaryWithOptions(r.Context(), chi.URLParam(r, "accountId"), *input.MoveExisting)
		} else {
			err = apierr.Invalid("PROVIDER_ACCOUNTS_UNAVAILABLE", "Account default options are unavailable in this build", nil)
		}
	} else {
		err = c.Svc.SetPrimary(r.Context(), chi.URLParam(r, "accountId"))
	}
	if err != nil {
		envelope.WriteError(w, r, accountAPIError(err))
		return
	}
	c.list(w, r)
}
func (c *ProviderAccountsController) remove(w http.ResponseWriter, r *http.Request) {
	c.removeAccount(w, r, false)
}
func (c *ProviderAccountsController) signOut(w http.ResponseWriter, r *http.Request) {
	c.removeAccount(w, r, true)
}
func (c *ProviderAccountsController) removeAccount(w http.ResponseWriter, r *http.Request, signOut bool) {
	if !c.ready(w, r) {
		return
	}
	var input ProviderAccountChangeRequest
	if r.ContentLength != 0 {
		if err := decodeAccountJSON(r, &input); err != nil {
			envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Invalid account change request", nil))
			return
		}
	}
	if err := c.Svc.Remove(r.Context(), chi.URLParam(r, "accountId"), input.ReplacementPrimaryID, signOut); err != nil {
		envelope.WriteError(w, r, accountAPIError(err))
		return
	}
	c.list(w, r)
}
func (c *ProviderAccountsController) sessionAccount(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	route, managed, err := c.Svc.SessionAccount(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, 200, SessionProviderAccountResponse{Managed: managed, Provider: route.Provider, AccountID: route.AccountID, LoginRequired: managed && route.AccountID == ""})
}
func (c *ProviderAccountsController) switchAccount(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	var input ProviderAccountChangeRequest
	if err := decodeAccountJSON(r, &input); err != nil || strings.TrimSpace(input.AccountID) == "" {
		envelope.WriteError(w, r, apierr.Invalid("ACCOUNT_REQUIRED", "Choose an account", nil))
		return
	}
	if err := c.Svc.Switch(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")), input.AccountID); err != nil {
		envelope.WriteError(w, r, accountAPIError(err))
		return
	}
	c.sessionAccount(w, r)
}
func loginView(login ports.ProviderLogin) ProviderLoginResponse {
	return ProviderLoginResponse{ID: login.ID, Provider: login.Provider, Mode: login.Mode, URL: login.URL, Code: login.Code, ExpiresIn: login.ExpiresIn, Status: login.Status, AccountID: login.AccountID}
}
func (c *ProviderAccountsController) loginReady(w http.ResponseWriter, r *http.Request) bool {
	if !c.ready(w, r) {
		return false
	}
	if c.Login == nil {
		envelope.WriteAPIError(w, r, 503, "service_unavailable", "PROVIDER_LOGIN_UNAVAILABLE", "Account sign-in is unavailable in this build", nil)
		return false
	}
	return true
}
func (c *ProviderAccountsController) startLogin(w http.ResponseWriter, r *http.Request) {
	if !c.loginReady(w, r) {
		return
	}
	var input ProviderLoginRequest
	if err := decodeAccountJSON(r, &input); err != nil || (input.Provider != "codex" && input.Provider != "claude") {
		envelope.WriteError(w, r, apierr.Invalid("PROVIDER_REQUIRED", "Choose Codex or Claude", nil))
		return
	}
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = "browser"
	}
	switch mode {
	case "browser":
	case "device":
		if input.Provider != "codex" {
			envelope.WriteError(w, r, apierr.Invalid("LOGIN_MODE_UNSUPPORTED", "Device login is available for Codex only", nil))
			return
		}
	case "import":
		if strings.TrimSpace(input.CredentialJSON) == "" {
			envelope.WriteError(w, r, apierr.Invalid("CREDENTIAL_JSON_REQUIRED", "Paste or choose a credential JSON file", nil))
			return
		}
	case "api_key":
		if strings.TrimSpace(input.APIKey) == "" || strings.TrimSpace(input.BaseURL) == "" {
			envelope.WriteError(w, r, apierr.Invalid("API_KEY_FIELDS_REQUIRED", "API key and base URL are required", nil))
			return
		}
	default:
		envelope.WriteError(w, r, apierr.Invalid("LOGIN_MODE_UNSUPPORTED", "Choose browser, device, API key, or JSON import", nil))
		return
	}
	var login ports.ProviderLogin
	var err error
	if mode == "browser" {
		login, err = c.Login.Start(r.Context(), input.Provider, input.AccountID)
	} else if modes, ok := c.Login.(interface {
		StartRequest(context.Context, string, string, string, ports.ProviderLoginInput) (ports.ProviderLogin, error)
	}); ok {
		login, err = modes.StartRequest(r.Context(), input.Provider, input.AccountID, mode, ports.ProviderLoginInput{APIKey: input.APIKey, BaseURL: input.BaseURL, Label: input.Label, CredentialJSON: input.CredentialJSON})
	} else {
		envelope.WriteError(w, r, apierr.Invalid("LOGIN_MODE_UNSUPPORTED", "This login method is unavailable in this build", nil))
		return
	}
	if err != nil {
		envelope.WriteError(w, r, accountAPIError(err))
		return
	}
	envelope.WriteJSON(w, 200, loginView(login))
}
func (c *ProviderAccountsController) loginStatus(w http.ResponseWriter, r *http.Request) {
	if !c.loginReady(w, r) {
		return
	}
	login, err := c.Login.Status(r.Context(), chi.URLParam(r, "loginId"))
	if err != nil {
		envelope.WriteError(w, r, accountAPIError(err))
		return
	}
	envelope.WriteJSON(w, 200, loginView(login))
}
func (c *ProviderAccountsController) cancelLogin(w http.ResponseWriter, r *http.Request) {
	if !c.loginReady(w, r) {
		return
	}
	if err := c.Login.Cancel(r.Context(), chi.URLParam(r, "loginId")); err != nil {
		envelope.WriteError(w, r, accountAPIError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeAccountJSON(r *http.Request, output any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 || !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		return errors.New("invalid account request body")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request must contain exactly one JSON object")
	}
	return nil
}

// providerAccountKind names an API key as one even when it was recorded before
// the account helper said so.
func providerAccountKind(a domain.ProviderAccount) string {
	if a.APIKey() {
		return "api_key"
	}
	return a.Kind
}
