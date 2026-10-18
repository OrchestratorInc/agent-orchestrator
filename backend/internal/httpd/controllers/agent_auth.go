package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

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

// AgentAuthController owns the safe native authentication routes.
type AgentAuthController struct {
	Svc AgentAuthService
}

// Register mounts the agent authentication routes.
func (c *AgentAuthController) Register(r chi.Router) {
	r.Get("/agents/auth-plans", c.list)
	r.Post("/agents/{agent}/auth", c.start)
	r.Post("/agents/{agent}/logout", c.logout)
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
