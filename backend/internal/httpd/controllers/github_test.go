package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/githubpat"
)

type invalidGitHubPATService struct{}

func (invalidGitHubPATService) StorePAT(context.Context, string) error { return nil }
func (invalidGitHubPATService) StoreOAuthToken(context.Context, string, string, time.Time, time.Time) error {
	return nil
}
func (invalidGitHubPATService) HasPAT(context.Context) bool { return true }

func (invalidGitHubPATService) Login(context.Context) string { return "" }

func (invalidGitHubPATService) DeletePAT(context.Context) error {
	return nil
}
func (invalidGitHubPATService) ListRepos(context.Context) ([]githubpat.Repo, error) {
	return nil, githubpat.ErrInvalidCredentials
}

type loginGitHubPATService struct {
	invalidGitHubPATService
	login string
}

func (s loginGitHubPATService) Login(context.Context) string { return s.login }

func TestGitHubStatusReportsConnectionAndLogin(t *testing.T) {
	for name, tc := range map[string]struct{ login, want string }{
		"signed in": {"octocat", `{"connected":true,"login":"octocat"}`},
		"unknown":   {"", `{"connected":true}`},
	} {
		router := chi.NewRouter()
		(&GitHubController{Svc: loginGitHubPATService{login: tc.login}}).Register(router)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/github/status", nil))
		if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != tc.want {
			t.Errorf("%s: status=%d body=%s, want %s", name, recorder.Code, recorder.Body.String(), tc.want)
		}
	}
}

func TestGitHubListReposReportsInvalidCredentials(t *testing.T) {
	router := chi.NewRouter()
	controller := GitHubController{Svc: invalidGitHubPATService{}}
	controller.Register(router)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/github/repos", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
	}
	var response envelope.APIError
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != "GITHUB_AUTH_INVALID" {
		t.Fatalf("code = %q, want GITHUB_AUTH_INVALID", response.Code)
	}
}
