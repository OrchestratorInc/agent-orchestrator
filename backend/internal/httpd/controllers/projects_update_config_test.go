package controllers_test

import (
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

func TestProjectsAPI_UpdateConfigNullAndEmptyString(t *testing.T) {
	srv := newTestServer(t)
	repo := gitRepo(t, "update-config-null")
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"update-config-null","config":{"defaultBranch":"main"}}`)
	if status != http.StatusCreated {
		t.Fatalf("seed project = %d, want 201; body=%s", status, body)
	}

	path := "/api/v1/projects/update-config-null/config"
	body, status, _ = doRequest(t, srv, http.MethodPatch, path, `{"canonicalRepoURL":null,"defaultBranch":null}`)
	if status != http.StatusOK {
		t.Fatalf("null patch = %d, want 200; body=%s", status, body)
	}
	var unchanged struct {
		Changes []projectsvc.ConfigChange `json:"changes"`
		Project struct {
			Config *domain.ProjectConfig `json:"config"`
		} `json:"project"`
	}
	mustJSON(t, body, &unchanged)
	if len(unchanged.Changes) != 0 || unchanged.Project.Config == nil || unchanged.Project.Config.DefaultBranch != "main" {
		t.Fatalf("null patch changed config: %s", body)
	}

	body, status, _ = doRequest(t, srv, http.MethodPatch, path, `{"defaultBranch":""}`)
	if status != http.StatusOK {
		t.Fatalf("empty string patch = %d, want 200; body=%s", status, body)
	}
	var cleared struct {
		Changes []projectsvc.ConfigChange `json:"changes"`
	}
	mustJSON(t, body, &cleared)
	if len(cleared.Changes) != 1 || cleared.Changes[0].From != "main" || cleared.Changes[0].To != "" {
		t.Fatalf("clear changes = %#v", cleared.Changes)
	}

	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/projects/update-config-null", "")
	if status != http.StatusOK {
		t.Fatalf("get project = %d, want 200; body=%s", status, body)
	}
	var got struct {
		Project struct {
			Config *domain.ProjectConfig `json:"config"`
		} `json:"project"`
	}
	mustJSON(t, body, &got)
	if got.Project.Config != nil && got.Project.Config.DefaultBranch != "" {
		t.Fatalf("empty string did not clear defaultBranch: %s", body)
	}
}
