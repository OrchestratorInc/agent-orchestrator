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
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/spawnpreflight"
)

type fakeSpawnPreflight struct {
	got    spawnpreflight.Request
	report spawnpreflight.Report
	err    error
}

func (f *fakeSpawnPreflight) Check(_ context.Context, req spawnpreflight.Request) (spawnpreflight.Report, error) {
	f.got = req
	return f.report, f.err
}

func TestSpawnPreflightRoute(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fake := &fakeSpawnPreflight{report: spawnpreflight.Report{
		Kind: "orchestrator", Harness: "codex", Checks: []spawnpreflight.Check{{
			Code: spawnpreflight.BlockerAgentNotInstalled, Blocking: true, Command: "npm install -g @openai/codex", InstallTarget: "codex",
		}},
	}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{SpawnPreflight: fake}, httpd.ControlDeps{}))
	defer srv.Close()

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/preflight",
		`{"projectId":"p1","kind":"orchestrator","resolution":{"blockers":["agent_not_installed"],"method":"install"}}`)
	if status != http.StatusOK {
		t.Fatalf("POST /sessions/preflight = %d, body=%s", status, body)
	}
	for _, want := range []string{`"code":"agent_not_installed"`, `"installTarget":"codex"`, `"blocking":true`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
	if fake.got.ProjectID != "p1" || fake.got.Resolution == nil || fake.got.Resolution.Method != "install" {
		t.Fatalf("request = %+v", fake.got)
	}

	if _, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/preflight", `{"projectId":"p1","kind":"worker","extra":1}`); status != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", status)
	}

	fake.err = apierr.Invalid("INVALID_KIND", "kind must be orchestrator or worker", nil)
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions/preflight", `{"projectId":"p1","kind":"reviewer"}`)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "INVALID_KIND") {
		t.Fatalf("invalid kind = %d %s", status, body)
	}
}

func TestSpawnPreflightRouteNotImplemented(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	defer srv.Close()
	if _, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/preflight", `{"projectId":"p1","kind":"worker"}`); status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", status)
	}
}
