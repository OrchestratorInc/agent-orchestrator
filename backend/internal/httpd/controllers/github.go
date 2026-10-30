package controllers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/githubpat"
)

// GitHubPATService is the controller-facing contract for local GitHub PAT management.
type GitHubPATService interface {
	HasPAT(ctx context.Context) bool
	// Login is the GitHub username AO is signed in as (a stored token or the
	// gh CLI), or "" when unknown.
	Login(ctx context.Context) string
	DeletePAT(ctx context.Context) error
	ListRepos(ctx context.Context) ([]githubpat.Repo, error)
	StartDeviceLogin(ctx context.Context) (githubpat.DeviceLogin, error)
	DeviceLoginStatus() githubpat.DeviceLogin
	CancelDeviceLogin()
	Owners(ctx context.Context) ([]githubpat.Owner, error)
	RepositoryAvailable(ctx context.Context, owner, name string) (bool, error)
}

// GitHubStatusResponse reports whether a token is stored and whom AO is signed
// in to GitHub as.
type GitHubStatusResponse struct {
	Connected bool   `json:"connected"`
	Login     string `json:"login,omitempty" description:"GitHub username AO is signed in as, from a stored token or the gh CLI; absent when it could not be resolved."`
}

// GitHubLoginResponse is the state of the GitHub device-flow sign-in. While
// pending, the user enters UserCode at VerificationURI.
type GitHubLoginResponse struct {
	State           string     `json:"state" enum:"idle,pending,succeeded,failed" description:"Progress of the sign-in."`
	UserCode        string     `json:"userCode,omitempty" description:"Code the user enters on GitHub; present while pending."`
	VerificationURI string     `json:"verificationUri,omitempty" description:"GitHub page where the user enters the code."`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty" description:"When the code stops working."`
	Login           string     `json:"login,omitempty" description:"GitHub username, once signed in."`
	AvatarURL       string     `json:"avatarUrl,omitempty" description:"GitHub avatar image URL, once signed in."`
	Error           string     `json:"error,omitempty" description:"Why the sign-in failed."`
}

func gitHubLoginResponse(login githubpat.DeviceLogin) GitHubLoginResponse {
	out := GitHubLoginResponse{
		State:           string(login.State),
		UserCode:        login.UserCode,
		VerificationURI: login.VerificationURI,
		Login:           login.Login,
		AvatarURL:       login.AvatarURL,
		Error:           login.Error,
	}
	if login.State == githubpat.DeviceLoginPending {
		expiresAt := login.ExpiresAt
		out.ExpiresAt = &expiresAt
	}
	return out
}

// GitHubOwnersResponse lists where a new repository can be created: the
// signed-in user first, then their organizations.
type GitHubOwnersResponse struct {
	Owners []githubpat.Owner `json:"owners"`
}

// GitHubRepositoryAvailabilityQuery names the repository to check.
type GitHubRepositoryAvailabilityQuery struct {
	Owner string `query:"owner" description:"Account or organization that would own the repository."`
	Name  string `query:"name" description:"Repository name."`
}

// GitHubRepositoryAvailabilityResponse reports whether owner/name is free.
type GitHubRepositoryAvailabilityResponse struct {
	Available bool `json:"available"`
}

// GitHubController owns the local GitHub PAT + repos routes.
type GitHubController struct {
	Svc GitHubPATService
}

// Register mounts the GitHub routes on the supplied router.
func (c *GitHubController) Register(r chi.Router) {
	r.Delete("/github/pat", c.deletePAT)
	r.Get("/github/status", c.status)
	r.Get("/github/repos", c.listRepos)
	r.Post("/github/login", c.startLogin)
	r.Get("/github/login", c.loginStatus)
	r.Delete("/github/login", c.cancelLogin)
	r.Get("/github/owners", c.owners)
	r.Get("/github/repository-availability", c.repositoryAvailability)
}

func (c *GitHubController) owners(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/github/owners")
		return
	}
	owners, err := c.Svc.Owners(r.Context())
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadGateway, "bad_gateway", "GITHUB_OWNERS_FAILED", "Could not load your GitHub accounts.", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, GitHubOwnersResponse{Owners: owners})
}

func (c *GitHubController) repositoryAvailability(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/github/repository-availability")
		return
	}
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if owner == "" || name == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_REQUEST", "Owner and repository name are required.", nil)
		return
	}
	available, err := c.Svc.RepositoryAvailable(r.Context(), owner, name)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadGateway, "bad_gateway", "GITHUB_REPOSITORY_CHECK_FAILED", "Could not check this repository name on GitHub.", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, GitHubRepositoryAvailabilityResponse{Available: available})
}

func (c *GitHubController) startLogin(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/github/login")
		return
	}
	login, err := c.Svc.StartDeviceLogin(r.Context())
	if errors.Is(err, githubpat.ErrDeviceLoginUnavailable) {
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "GITHUB_LOGIN_UNAVAILABLE", "GitHub sign-in is not configured in this build.", nil)
		return
	}
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadGateway, "bad_gateway", "GITHUB_LOGIN_FAILED", "Could not reach GitHub to start sign-in.", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, gitHubLoginResponse(login))
}

func (c *GitHubController) loginStatus(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/github/login")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, gitHubLoginResponse(c.Svc.DeviceLoginStatus()))
}

func (c *GitHubController) cancelLogin(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodDelete, "/api/v1/github/login")
		return
	}
	c.Svc.CancelDeviceLogin()
	envelope.WriteJSON(w, http.StatusOK, gitHubLoginResponse(c.Svc.DeviceLoginStatus()))
}

func (c *GitHubController) deletePAT(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodDelete, "/api/v1/github/pat")
		return
	}
	if err := c.Svc.DeletePAT(r.Context()); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (c *GitHubController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/github/status")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, GitHubStatusResponse{Connected: c.Svc.HasPAT(r.Context()), Login: c.Svc.Login(r.Context())})
}

func (c *GitHubController) listRepos(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/github/repos")
		return
	}
	repos, err := c.Svc.ListRepos(r.Context())
	if err != nil {
		if errors.Is(err, githubpat.ErrInvalidCredentials) {
			envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "GITHUB_AUTH_INVALID", "GitHub authorization expired. Reconnect GitHub to continue.", nil)
			return
		}
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]any{"repos": repos})
}
