package controllers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentaccounts"
)

// AgentAccountService is the HTTP boundary of the Claude Code and Codex
// subscription accounts AO launches agents with.
type AgentAccountService interface {
	List(context.Context, bool) ([]agentaccounts.AccountView, error)
	StartLogin(context.Context, domain.AgentAccountProvider, agentaccounts.LoginRequest) (agentaccounts.LoginStart, error)
	StartSignInAgain(context.Context, string, agentaccounts.LoginRequest) (agentaccounts.LoginStart, error)
	VerifyLogin(context.Context, string) (agentaccounts.LoginStatus, *agentaccounts.AccountView, error)
	CancelLogin(context.Context, string) error
	AddAPIKeyAccount(context.Context, domain.AgentAccountProvider, string, string) (agentaccounts.AccountView, error)
	ReplaceAPIKey(context.Context, string, string) (agentaccounts.AccountView, error)
	SetDefault(context.Context, string) error
	Remove(context.Context, string) error
	ConsumeResetCredit(context.Context, string, string) (domain.CodexResetCreditOutcome, error)
	Usage(context.Context, int) ([]agentaccounts.AccountUsage, error)
	SessionAccounts(context.Context, domain.SessionID) (map[domain.AgentAccountProvider]string, error)
}

// AgentAccountSessionMover moves a session's agent to another account.
type AgentAccountSessionMover interface {
	StartAccountTransition(context.Context, domain.SessionID, string, bool) (domain.SessionInterfaceTransition, error)
}

// AgentAccountsController exposes accounts, sign-in, limits, usage, and the
// account of each session.
type AgentAccountsController struct {
	Svc      AgentAccountService
	Sessions AgentAccountSessionMover
}

// Register adds the account routes.
func (c *AgentAccountsController) Register(r chi.Router) {
	r.Get("/agents/accounts", c.list)
	r.Get("/agents/accounts/usage", c.usage)
	r.Post("/agents/accounts/login", c.startLogin)
	r.Post("/agents/accounts/login/{operationId}/verify", c.verifyLogin)
	r.Post("/agents/accounts/login/{operationId}/cancel", c.cancelLogin)
	r.Post("/agents/accounts/api-keys", c.addAPIKey)
	r.Get("/agents/accounts/sessions/{sessionId}", c.sessionAccounts)
	r.Put("/agents/accounts/sessions/{sessionId}", c.switchSession)
	c.registerAccount(r)
}

// registerAccount adds the routes that act on one account.
func (c *AgentAccountsController) registerAccount(r chi.Router) {
	r.Put("/agents/accounts/{accountId}/api-key", c.replaceAPIKey)
	r.Post("/agents/accounts/{accountId}/login", c.signInAgain)
	r.Post("/agents/accounts/{accountId}/default", c.setDefault)
	r.Get("/agents/accounts/{accountId}/connectors", c.connectors)
	r.Post("/agents/accounts/{accountId}/reset-credit", c.consumeResetCredit)
	r.Delete("/agents/accounts/{accountId}", c.remove)
}

func (c *AgentAccountsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/agents/accounts")
		return
	}
	c.writeAccounts(w, r, r.URL.Query().Get("refresh") == "true" || r.URL.Query().Get("refresh") == "1")
}

func (c *AgentAccountsController) writeAccounts(w http.ResponseWriter, r *http.Request, refresh bool) {
	views, err := c.Svc.List(r.Context(), refresh)
	if err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	out := AgentAccountsResponse{Accounts: make([]AgentAccountResponse, 0, len(views))}
	for _, view := range views {
		out.Accounts = append(out.Accounts, agentAccountResponse(view))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func agentAccountResponse(view agentaccounts.AccountView) AgentAccountResponse {
	kind := view.Account.Kind
	if kind == "" {
		kind = domain.AgentAccountKindSubscription
	}
	out := AgentAccountResponse{
		ID: view.Account.ID, Provider: view.Account.Provider, Device: view.Account.Device(), Kind: kind, Name: view.Account.Name, KeyHint: view.Account.KeyHint,
		Email: view.Account.Email, Plan: view.Account.Plan, Default: view.Default, SignedIn: view.SignedIn, LoggedIn: view.LoggedIn,
		Problem: view.IdentityErr, ProblemKind: view.IdentityProblem, Limits: view.Limits, SameLoginAs: view.SameLoginAs, OrgID: view.OrgID, OrgName: view.OrgName,
	}
	if out.Problem == "" && view.SignedIn {
		out.Problem, out.ProblemKind = view.LimitsErr, view.LimitsProblem
	}
	if !view.Account.CreatedAt.IsZero() {
		created := view.Account.CreatedAt
		out.CreatedAt = &created
	}
	return out
}

func (c *AgentAccountsController) startLogin(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/agents/accounts/login")
		return
	}
	var in StartAgentAccountLoginRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	start, err := c.Svc.StartLogin(r.Context(), in.Provider, agentaccounts.LoginRequest{Method: in.Method, Secret: in.Secret, Name: in.Name})
	if err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	writeAgentAccountLoginStart(w, start)
}

func (c *AgentAccountsController) signInAgain(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/agents/accounts/{accountId}/login")
		return
	}
	// The body is optional: none opens the CLI's own sign-in in a terminal.
	var in SignInAgentAccountAgainRequest
	if err := decodeJSONStrict(r, &in); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	start, err := c.Svc.StartSignInAgain(r.Context(), strings.TrimSpace(chi.URLParam(r, "accountId")), agentaccounts.LoginRequest{Method: in.Method, Secret: in.Secret})
	if err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	writeAgentAccountLoginStart(w, start)
}

func writeAgentAccountLoginStart(w http.ResponseWriter, start agentaccounts.LoginStart) {
	out := StartAgentAccountLoginResponse{OperationID: start.OperationID, Provider: start.Provider, AuthOperationID: start.AuthOperationID}
	if start.Terminal.HandleID != "" {
		out.ShellTerminal = &CodexAccountLoginTerminalResponse{HandleID: start.Terminal.HandleID, Title: start.Terminal.Title, CreatedAt: start.Terminal.CreatedAt}
	}
	if start.Account != nil {
		account := agentAccountResponse(*start.Account)
		out.Account = &account
	}
	envelope.WriteJSON(w, http.StatusAccepted, out)
}

func (c *AgentAccountsController) verifyLogin(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/agents/accounts/login/{operationId}/verify")
		return
	}
	status, view, err := c.Svc.VerifyLogin(r.Context(), strings.TrimSpace(chi.URLParam(r, "operationId")))
	if err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	out := VerifyAgentAccountLoginResponse{Status: status}
	if view != nil {
		account := agentAccountResponse(*view)
		out.Account = &account
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *AgentAccountsController) cancelLogin(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/agents/accounts/login/{operationId}/cancel")
		return
	}
	if err := c.Svc.CancelLogin(r.Context(), strings.TrimSpace(chi.URLParam(r, "operationId"))); err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *AgentAccountsController) setDefault(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/agents/accounts/{accountId}/default")
		return
	}
	if err := c.Svc.SetDefault(r.Context(), strings.TrimSpace(chi.URLParam(r, "accountId"))); err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	c.writeAccounts(w, r, false)
}

// AgentAccountConnectorLister reads the connectors an account's sessions load.
// It is optional so account services without connectors need not stub it.
type AgentAccountConnectorLister interface {
	Connectors(ctx context.Context, accountID string, refresh bool) (domain.AgentConnectorsReading, error)
}

// connectors lists an account's connectors (Claude: its claude.ai connectors),
// reusing a recent reading unless refresh=true.
func (c *AgentAccountsController) connectors(w http.ResponseWriter, r *http.Request) {
	lister, ok := c.Svc.(AgentAccountConnectorLister)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, "GET", "/api/v1/agents/accounts/{accountId}/connectors")
		return
	}
	refresh := false
	if raw := strings.TrimSpace(r.URL.Query().Get("refresh")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_REFRESH", "refresh must be true or false", nil)
			return
		}
		refresh = parsed
	}
	reading, err := lister.Connectors(r.Context(), strings.TrimSpace(chi.URLParam(r, "accountId")), refresh)
	if err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, agentAccountConnectorsResponse(reading))
}

func agentAccountConnectorsResponse(reading domain.AgentConnectorsReading) AgentAccountConnectorsResponse {
	out := AgentAccountConnectorsResponse{
		AccountID:   reading.AccountID,
		Unavailable: string(reading.Unavailable),
		Connectors:  make([]AgentAccountConnector, 0, len(reading.Connectors)),
		Error:       reading.Error,
	}
	for _, connector := range reading.Connectors {
		out.Connectors = append(out.Connectors, AgentAccountConnector{Name: connector.Name, Status: string(connector.Status)})
	}
	if !reading.CheckedAt.IsZero() {
		checked := reading.CheckedAt.UTC()
		out.CheckedAt = &checked
	}
	return out
}

func (c *AgentAccountsController) remove(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/agents/accounts/{accountId}")
		return
	}
	if err := c.Svc.Remove(r.Context(), strings.TrimSpace(chi.URLParam(r, "accountId"))); err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	c.writeAccounts(w, r, false)
}

func (c *AgentAccountsController) addAPIKey(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/agents/accounts/api-keys")
		return
	}
	var in AddAgentAccountAPIKeyRequest
	if err := decodeJSONStrict(r, &in); err != nil || strings.TrimSpace(in.APIKey) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "API_KEY_REQUIRED", "An API key is required", nil)
		return
	}
	if _, err := c.Svc.AddAPIKeyAccount(r.Context(), in.Provider, in.Name, in.APIKey); err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	c.writeAccounts(w, r, false)
}

func (c *AgentAccountsController) replaceAPIKey(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/agents/accounts/{accountId}/api-key")
		return
	}
	var in ReplaceAgentAccountAPIKeyRequest
	if err := decodeJSONStrict(r, &in); err != nil || strings.TrimSpace(in.APIKey) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "API_KEY_REQUIRED", "An API key is required", nil)
		return
	}
	if _, err := c.Svc.ReplaceAPIKey(r.Context(), strings.TrimSpace(chi.URLParam(r, "accountId")), in.APIKey); err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	c.writeAccounts(w, r, false)
}

func (c *AgentAccountsController) consumeResetCredit(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/agents/accounts/{accountId}/reset-credit")
		return
	}
	var in ConsumeAgentAccountResetCreditRequest
	if err := decodeJSONStrict(r, &in); err != nil || strings.TrimSpace(in.IdempotencyKey) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "IDEMPOTENCY_KEY_REQUIRED", "Idempotency key is required", nil)
		return
	}
	outcome, err := c.Svc.ConsumeResetCredit(r.Context(), strings.TrimSpace(chi.URLParam(r, "accountId")), in.IdempotencyKey)
	if err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ConsumeAgentAccountResetCreditResponse{Outcome: outcome})
}

func (c *AgentAccountsController) usage(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/agents/accounts/usage")
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 30
	}
	rows, err := c.Svc.Usage(r.Context(), days)
	if err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	out := AgentAccountUsageResponse{Days: days, Usage: make([]AgentAccountUsageRow, 0, len(rows))}
	for _, row := range rows {
		item := AgentAccountUsageRow{AccountID: row.AccountID, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, CostNanos: row.CostNanos,
			Daily: make([]AgentAccountUsageDay, 0, len(row.Days)), Models: make([]AgentAccountUsageByModel, 0, len(row.Models))}
		for _, d := range row.Days {
			item.Daily = append(item.Daily, AgentAccountUsageDay{Day: d.Day, Tokens: d.Tokens, CostNanos: d.CostNanos})
		}
		for _, m := range row.Models {
			item.Models = append(item.Models, AgentAccountUsageByModel{Model: m.Model, Tokens: m.Tokens, CostNanos: m.CostNanos})
		}
		out.Usage = append(out.Usage, item)
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *AgentAccountsController) sessionAccounts(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/agents/accounts/sessions/{sessionId}")
		return
	}
	accounts, err := c.Svc.SessionAccounts(r.Context(), domain.SessionID(strings.TrimSpace(chi.URLParam(r, "sessionId"))))
	if err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionAgentAccountsResponse{
		Claude: accounts[domain.AgentAccountProviderClaude], Codex: accounts[domain.AgentAccountProviderCodex],
	})
}

func (c *AgentAccountsController) switchSession(w http.ResponseWriter, r *http.Request) {
	if c.Sessions == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/agents/accounts/sessions/{sessionId}")
		return
	}
	var in SwitchSessionAgentAccountRequest
	if err := decodeJSONStrict(r, &in); err != nil || strings.TrimSpace(in.AccountID) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "accountId is required", nil)
		return
	}
	transition, err := c.Sessions.StartAccountTransition(r.Context(), domain.SessionID(strings.TrimSpace(chi.URLParam(r, "sessionId"))), strings.TrimSpace(in.AccountID), in.StopTurn)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, StartSessionInterfaceTransitionResponse{
		OK: true, SessionID: transition.SessionID, Transition: sessionInterfaceTransitionView(transition),
	})
}

// agentAccountUnsharedMessage is shown when an account folder cannot link to
// this computer's history. When Windows refused the link for want of its own
// Developer Mode (not Orchestrator's), the message says where to turn it on.
const (
	agentAccountUnsharedMessage      = "Orchestrator cannot link this account's folder to this computer's history."
	agentAccountDeveloperModeMessage = "Turn on Windows Developer Mode to use this account: search Windows Settings for Developer Mode (System > Advanced on Windows 11 25H2 and later)."
)

func writeAgentAccountError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ports.ErrAgentAccountUnknown):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "AGENT_ACCOUNT_NOT_FOUND", "Unknown agent account", nil)
	case errors.Is(err, ports.ErrAgentAccountLoginUnknown):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "AGENT_ACCOUNT_LOGIN_NOT_FOUND", "Unknown or finished sign-in", nil)
	case errors.Is(err, ports.ErrAgentAccountProviderMismatch):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "AGENT_ACCOUNT_PROVIDER_INVALID", "Accounts are available for Claude Code and Codex only", nil)
	case errors.Is(err, ports.ErrAgentAccountKeyRejected):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "AGENT_ACCOUNT_KEY_REJECTED", "The provider did not accept this key or token. Check it and try again.", nil)
	case errors.Is(err, ports.ErrAgentAccountKeyUnchecked):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "AGENT_ACCOUNT_KEY_UNCHECKED", "Orchestrator could not reach the provider to check this API key. Try again in a moment.", nil)
	case errors.Is(err, ports.ErrAgentAccountKindMismatch):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "AGENT_ACCOUNT_KIND_MISMATCH", "This account does not support that action", nil)
	case errors.Is(err, ports.ErrAgentAccountMethodInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "AGENT_ACCOUNT_METHOD_INVALID", "This sign-in method cannot add an account here", nil)
	case errors.Is(err, ports.ErrAgentAccountSignInUnavailable):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "AGENT_ACCOUNT_SIGN_IN_UNAVAILABLE", "The in-app sign-in is unavailable. Use the terminal sign-in instead.", nil)
	case errors.Is(err, ports.ErrAgentAccountNeedsDeveloperMode):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "AGENT_ACCOUNT_UNSHARED", agentAccountDeveloperModeMessage, map[string]any{"windowsDeveloperMode": true})
	case errors.Is(err, ports.ErrAgentAccountUnshared):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "AGENT_ACCOUNT_UNSHARED", agentAccountUnsharedMessage, nil)
	case errors.Is(err, ports.ErrAgentAccountInUse):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "AGENT_ACCOUNT_IN_USE", "Orchestrator could not stop an agent that uses this account, so it kept the account. Stop that agent, then remove the account again.", nil)
	case errors.Is(err, ports.ErrSessionNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "session not found", nil)
	case errors.Is(err, ports.ErrAgentAccountDeviceFixed):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "AGENT_ACCOUNT_DEVICE_FIXED", "This computer's own login is managed by its CLI, not Orchestrator", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}
