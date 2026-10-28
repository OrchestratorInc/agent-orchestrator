package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systemcheck"
)

// SystemChecker is the controller-facing contract for the lightweight startup
// preflight the desktop loading screen runs before showing the board.
type SystemChecker interface {
	CheckStartup(ctx context.Context) (systemcheck.Report, error)
	CheckGitHubAuth(ctx context.Context) (systemcheck.Requirement, error)
	OpenGitHubAuthTerminal(ctx context.Context) (shellterm.ShellTerminal, error)
	StartGitHubDeviceLogin(ctx context.Context) (systemcheck.GitHubDeviceLogin, error)
	GitHubDeviceLoginStatus(ctx context.Context) (systemcheck.GitHubDeviceLogin, error)
	CancelGitHubDeviceLogin(ctx context.Context) (systemcheck.GitHubDeviceLogin, error)
}

// SystemController owns the /system routes.
type SystemController struct {
	Checks SystemChecker
}

// Register mounts the system requirements route on the supplied router.
func (c *SystemController) Register(r chi.Router) {
	r.Get("/system/requirements", c.requirements)
	r.Get("/system/github-auth", c.githubAuth)
	r.Post("/system/github-auth/terminal", c.openGitHubAuthTerminal)
	r.Post("/system/github-auth/device", c.startGitHubDeviceLogin)
	r.Get("/system/github-auth/device", c.githubDeviceLoginStatus)
	r.Delete("/system/github-auth/device", c.cancelGitHubDeviceLogin)
}

func (c *SystemController) openGitHubAuthTerminal(w http.ResponseWriter, r *http.Request) {
	if c.Checks == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/system/github-auth/terminal")
		return
	}
	terminal, err := c.Checks.OpenGitHubAuthTerminal(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ShellTerminalEnvelope{ShellTerminal: shellTerminalResponse(terminal)})
}

func (c *SystemController) githubAuth(w http.ResponseWriter, r *http.Request) {
	if c.Checks == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/system/github-auth")
		return
	}
	requirement, err := c.Checks.CheckGitHubAuth(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, requirement)
}

func (c *SystemController) requirements(w http.ResponseWriter, r *http.Request) {
	if c.Checks == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/system/requirements")
		return
	}
	report, err := c.Checks.CheckStartup(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, report)
}

func (c *SystemController) startGitHubDeviceLogin(w http.ResponseWriter, r *http.Request) {
	c.deviceLogin(w, r, http.MethodPost, http.StatusCreated, func(ctx context.Context) (systemcheck.GitHubDeviceLogin, error) {
		return c.Checks.StartGitHubDeviceLogin(ctx)
	})
}

func (c *SystemController) githubDeviceLoginStatus(w http.ResponseWriter, r *http.Request) {
	c.deviceLogin(w, r, http.MethodGet, http.StatusOK, func(ctx context.Context) (systemcheck.GitHubDeviceLogin, error) {
		return c.Checks.GitHubDeviceLoginStatus(ctx)
	})
}

func (c *SystemController) cancelGitHubDeviceLogin(w http.ResponseWriter, r *http.Request) {
	c.deviceLogin(w, r, http.MethodDelete, http.StatusOK, func(ctx context.Context) (systemcheck.GitHubDeviceLogin, error) {
		return c.Checks.CancelGitHubDeviceLogin(ctx)
	})
}

func (c *SystemController) deviceLogin(w http.ResponseWriter, r *http.Request, method string, status int, call func(context.Context) (systemcheck.GitHubDeviceLogin, error)) {
	if c.Checks == nil {
		apispec.NotImplemented(w, r, method, "/api/v1/system/github-auth/device")
		return
	}
	login, err := call(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, status, login)
}
