package controllers_test

import (
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

func TestProjectsAPI_UpdateConfigRejectsInvalidDefaultBranch(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "dry run", body: `{"defaultBranch":"bad..name","dryRun":true}`},
		{name: "write", body: `{"defaultBranch":"bad..name"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			repo := gitRepo(t, "update-config-invalid-branch")
			body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"update-config-invalid-branch","config":{"defaultBranch":"main"}}`)
			if status != http.StatusCreated {
				t.Fatalf("seed project = %d, want 201; body=%s", status, body)
			}

			path := "/api/v1/projects/update-config-invalid-branch"
			body, status, _ = doRequest(t, srv, http.MethodPatch, path+"/config", tc.body)
			if status != http.StatusBadRequest {
				t.Errorf("invalid branch patch = %d, want 400; body=%s", status, body)
			} else {
				assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_PROJECT_CONFIG")
			}

			body, status, _ = doRequest(t, srv, http.MethodGet, path, "")
			if status != http.StatusOK {
				t.Fatalf("get project = %d, want 200; body=%s", status, body)
			}
			var got struct {
				Project struct {
					Config *domain.ProjectConfig `json:"config"`
				} `json:"project"`
			}
			mustJSON(t, body, &got)
			if got.Project.Config == nil || got.Project.Config.DefaultBranch != "main" {
				t.Fatalf("invalid branch patch changed saved config: %s", body)
			}
		})
	}
}

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
