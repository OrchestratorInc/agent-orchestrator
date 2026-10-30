package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systemcheck"
)

type fakeSystemChecker struct {
	report    systemcheck.Report
	err       error
	calls     int
	auth      systemcheck.Requirement
	authErr   error
	authCalls int
}

func (f *fakeSystemChecker) CheckGitHubAuth(context.Context) (systemcheck.Requirement, error) {
	f.authCalls++
	return f.auth, f.authErr
}

func (f *fakeSystemChecker) CheckStartup(context.Context) (systemcheck.Report, error) {
	f.calls++
	return f.report, f.err
}

func TestGetGitHubAuthRequirement(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	checker := &fakeSystemChecker{auth: systemcheck.Requirement{
		ID: "github-auth", Label: "GitHub access", Required: false, Satisfied: false, Detail: "Sign in.",
	}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		SystemChecks: checker,
	}, httpd.ControlDeps{}))
	defer srv.Close()

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/system/github-auth", "")
	if status != http.StatusOK {
		t.Fatalf("GET /system/github-auth = %d, body=%s", status, body)
	}
	for _, want := range []string{`"id":"github-auth"`, `"satisfied":false`, `"required":false`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
	if checker.authCalls != 1 {
		t.Fatalf("auth calls = %d, want 1", checker.authCalls)
	}
}

func TestGetSystemRequirements(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	checker := &fakeSystemChecker{report: systemcheck.Report{
		Ready: false,
		Requirements: []systemcheck.Requirement{
			{ID: "git", Label: "git", Satisfied: true, Required: true, Detail: "/usr/bin/git"},
			{ID: "tmux", Label: "tmux", Satisfied: true, Required: true, Detail: "/usr/bin/tmux"},
			{ID: "github-auth", Label: "GitHub access", Satisfied: false, Required: false, Detail: "Sign in to GitHub."},
		},
	}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		SystemChecks: checker,
	}, httpd.ControlDeps{}))
	defer srv.Close()

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/system/requirements", "")
	if status != http.StatusOK {
		t.Fatalf("GET /system/requirements = %d, body=%s", status, body)
	}
	for _, want := range []string{`"ready":false`, `"id":"git"`, `"id":"tmux"`, `"id":"github-auth"`, `"required":false`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
	if checker.calls != 1 {
		t.Fatalf("calls = %d, want 1", checker.calls)
	}
}

func TestGetSystemRequirements_NotImplemented(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	defer srv.Close()

	_, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/system/requirements", "")
	if status != http.StatusNotImplemented {
		t.Fatalf("GET /system/requirements = %d, want %d", status, http.StatusNotImplemented)
	}
}
