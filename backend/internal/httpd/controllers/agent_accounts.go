package controllers

import (
	"context"
	"errors"
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
	StartLogin(context.Context, domain.AgentAccountProvider) (agentaccounts.LoginStart, error)
	StartSignInAgain(context.Context, string) (agentaccounts.LoginStart, error)
	VerifyLogin(context.Context, string) (agentaccounts.LoginStatus, *agentaccounts.AccountView, error)
	CancelLogin(context.Context, string) error
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
	r.Get("/agents/accounts/sessions/{sessionId}", c.sessionAccounts)
	r.Put("/agents/accounts/sessions/{sessionId}", c.switchSession)
	r.Post("/agents/accounts/{accountId}/login", c.signInAgain)
	r.Post("/agents/accounts/{accountId}/default", c.setDefault)
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
	out := AgentAccountResponse{
		ID: view.Account.ID, Provider: view.Account.Provider, Device: view.Account.Device(),
		Email: view.Account.Email, Plan: view.Account.Plan, Default: view.Default, SignedIn: view.SignedIn,
		Problem: view.IdentityErr, Limits: view.Limits,
	}
	if out.Problem == "" && view.SignedIn {
		out.Problem = view.LimitsErr
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
	start, err := c.Svc.StartLogin(r.Context(), in.Provider)
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
	start, err := c.Svc.StartSignInAgain(r.Context(), strings.TrimSpace(chi.URLParam(r, "accountId")))
	if err != nil {
		writeAgentAccountError(w, r, err)
		return
	}
	writeAgentAccountLoginStart(w, start)
}

func writeAgentAccountLoginStart(w http.ResponseWriter, start agentaccounts.LoginStart) {
	envelope.WriteJSON(w, http.StatusAccepted, StartAgentAccountLoginResponse{
		OperationID: start.OperationID, Provider: start.Provider,
		ShellTerminal: CodexAccountLoginTerminalResponse{HandleID: start.Terminal.HandleID, Title: start.Terminal.Title, CreatedAt: start.Terminal.CreatedAt},
	})
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
// this computer's history; Windows allows the links only in Developer Mode.
const agentAccountUnsharedMessage = "AO cannot link this account's folder to this computer's history. On Windows, turn on Developer Mode."

func writeAgentAccountError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ports.ErrAgentAccountUnknown):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "AGENT_ACCOUNT_NOT_FOUND", "Unknown agent account", nil)
	case errors.Is(err, ports.ErrAgentAccountLoginUnknown):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "AGENT_ACCOUNT_LOGIN_NOT_FOUND", "Unknown or finished sign-in", nil)
	case errors.Is(err, ports.ErrAgentAccountProviderMismatch):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "AGENT_ACCOUNT_PROVIDER_INVALID", "Accounts are available for Claude Code and Codex only", nil)
	case errors.Is(err, ports.ErrAgentAccountUnshared):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "AGENT_ACCOUNT_UNSHARED", agentAccountUnsharedMessage, nil)
	case errors.Is(err, ports.ErrAgentAccountInUse):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "AGENT_ACCOUNT_IN_USE", "AO could not stop an agent that uses this account, so it kept the account. Stop that agent, then remove the account again.", nil)
	case errors.Is(err, ports.ErrAgentAccountDeviceFixed):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "AGENT_ACCOUNT_DEVICE_FIXED", "This computer's own login is managed by its CLI, not AO", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}
