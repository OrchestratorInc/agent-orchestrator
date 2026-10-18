package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The role catalog query is only meaningful against a project (the pin lives
// in project config): an unknown role or a role without projectId is a 400,
// matching the OpenAPI description, not a silently unscoped catalog.
func TestAgentModelsRoleQueryValidation(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := &fakeAgentCatalog{models: ports.AgentModelCatalog{AgentID: "codex"}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Agents: catalog}, httpd.ControlDeps{}))
	defer srv.Close()

	for _, tc := range []struct {
		name     string
		method   string
		path     string
		wantCode string
	}{
		{name: "role without project", method: http.MethodGet, path: "/api/v1/agents/codex/models?role=worker", wantCode: "PROJECT_REQUIRED"},
		{name: "role without project on refresh", method: http.MethodPost, path: "/api/v1/agents/codex/models/refresh?role=reviewer", wantCode: "PROJECT_REQUIRED"},
		{name: "unknown role", method: http.MethodGet, path: "/api/v1/agents/codex/models?projectId=proj-1&role=reviewer2", wantCode: "INVALID_ROLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, status, _ := doRequest(t, srv, tc.method, tc.path, "")
			if status != http.StatusBadRequest || !strings.Contains(string(body), tc.wantCode) {
				t.Fatalf("%s %s = %d %s, want 400 %s", tc.method, tc.path, status, body, tc.wantCode)
			}
		})
	}
}
