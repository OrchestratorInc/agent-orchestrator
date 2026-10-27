package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentauth"
)

// AgentAuthService exposes only fixed, daemon-owned authentication plans.
type AgentAuthService interface {
	Plans(context.Context) []agentauth.Plan
	Start(ctx context.Context, agentID string) (agentauth.StartResult, error)
	Logout(ctx context.Context, agentID string) (agentauth.StartResult, error)
}

// AgentUILoginService exposes background authentication without raw output or
// commands. Kept separate so older services can continue offering terminal login.
type AgentUILoginService interface {
	StartLogin(context.Context, string, string, string) (agentauth.LoginSnapshot, error)
	Login(context.Context, string, string) (agentauth.LoginSnapshot, error)
	CancelLogin(context.Context, string, string) (agentauth.LoginSnapshot, error)
	ActOnLogin(context.Context, string, string, string) (agentauth.LoginSnapshot, error)
	SubmitLoginCode(context.Context, string, string, string) (agentauth.LoginSnapshot, error)
}

// AgentAuthController owns the safe native authentication routes.
type AgentAuthController struct {
	Svc AgentAuthService
}

// Register mounts the agent authentication routes.
func (c *AgentAuthController) Register(r chi.Router) {
	r.Get("/agents/auth-plans", c.list)
	r.Post("/agents/{agent}/auth", c.start)
	r.Post("/agents/{agent}/logout", c.logout)
	r.Post("/agents/{agent}/auth/operations", c.startUILogin)
	r.Get("/agents/{agent}/auth/operations/{operationId}", c.uiLogin)
	r.Delete("/agents/{agent}/auth/operations/{operationId}", c.uiLogin)
	r.Post("/agents/{agent}/auth/operations/{operationId}/actions", c.uiLoginAction)
	r.Post("/agents/{agent}/auth/operations/{operationId}/code", c.submitUILoginCode)
}

func (c *AgentAuthController) uiLoginAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	svc, ok := c.Svc.(AgentUILoginService)
	if !ok {
		apispec.NotImplemented(w, r, r.Method, "/api/v1/agents/{agent}/auth/operations/{operationId}/actions")
		return
	}
	var req AgentLoginActionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Invalid login action.", nil))
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Expected one login action.", nil))
		return
	}
	if !validLoginAction(req.Action) {
		envelope.WriteError(w, r, apierr.Invalid("AGENT_AUTH_ACTION_INVALID", "Choose a supported login menu action.", nil))
		return
	}
	result, err := svc.ActOnLogin(r.Context(), chi.URLParam(r, "agent"), chi.URLParam(r, "operationId"), req.Action)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}

// validLoginAction allows fixed menu navigation and the digits 1-9 for
// numbered native pickers; the service still rejects digits outside the
// recognized option list.
func validLoginAction(action string) bool {
	switch action {
	case "previous", "next", "select":
		return true
	}
	return len(action) == 1 && action >= "1" && action <= "9"
}

func (c *AgentAuthController) submitUILoginCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	svc, ok := c.Svc.(AgentUILoginService)
	if !ok {
		apispec.NotImplemented(w, r, r.Method, "/api/v1/agents/{agent}/auth/operations/{operationId}/code")
		return
	}
	var req SubmitAgentUILoginCodeRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 20*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Invalid sign-in code request.", nil))
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Expected one sign-in code request.", nil))
		return
	}
	result, err := svc.SubmitLoginCode(r.Context(), chi.URLParam(r, "agent"), chi.URLParam(r, "operationId"), req.Code)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, result)
}

func (c *AgentAuthController) startUILogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	svc, ok := c.Svc.(AgentUILoginService)
	if !ok {
		apispec.NotImplemented(w, r, r.Method, "/api/v1/agents/{agent}/auth/operations")
		return
	}
	var req StartAgentUILoginRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 20*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Invalid login request.", nil))
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		envelope.WriteError(w, r, apierr.Invalid("INVALID_JSON", "Expected one login request.", nil))
		return
	}
	result, err := svc.StartLogin(r.Context(), chi.URLParam(r, "agent"), req.Method, req.Secret)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, result)
}

func (c *AgentAuthController) uiLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	svc, ok := c.Svc.(AgentUILoginService)
	if !ok {
		apispec.NotImplemented(w, r, r.Method, "/api/v1/agents/{agent}/auth/operations/{operationId}")
		return
	}
	action := svc.Login
	if r.Method == http.MethodDelete {
		action = svc.CancelLogin
	}
	result, err := action(r.Context(), chi.URLParam(r, "agent"), chi.URLParam(r, "operationId"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}

func (c *AgentAuthController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/agents/auth-plans")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListAgentAuthPlansResponse{Plans: c.Svc.Plans(r.Context())})
}

func (c *AgentAuthController) start(w http.ResponseWriter, r *http.Request) {
	c.startFlow(w, r, false)
}

func (c *AgentAuthController) logout(w http.ResponseWriter, r *http.Request) {
	c.startFlow(w, r, true)
}

func (c *AgentAuthController) startFlow(w http.ResponseWriter, r *http.Request, logout bool) {
	if c.Svc == nil {
		path := "/api/v1/agents/{agent}/auth"
		if logout {
			path = "/api/v1/agents/{agent}/logout"
		}
		apispec.NotImplemented(w, r, http.MethodPost, path)
		return
	}
	start := c.Svc.Start
	if logout {
		start = c.Svc.Logout
	}
	result, err := start(r.Context(), chi.URLParam(r, "agent"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, StartAgentAuthResponse{
		AgentID:       result.AgentID,
		Action:        result.Action,
		Guidance:      result.Guidance,
		TerminalInput: result.TerminalInput,
		Terminal:      shellTerminalResponse(result.Terminal),
	})
}
