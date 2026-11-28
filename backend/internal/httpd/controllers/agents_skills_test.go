package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

type skillsProjects struct {
	projectsvc.Manager
	paths map[domain.ProjectID]string
}

func (p skillsProjects) Get(_ context.Context, id domain.ProjectID) (projectsvc.GetResult, error) {
	path, ok := p.paths[id]
	if !ok {
		return projectsvc.GetResult{}, os.ErrNotExist
	}
	return projectsvc.GetResult{Status: "ok", Project: &projectsvc.Project{ID: id, Path: path}}, nil
}

func writeSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\ndescription: "+name+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListAgentSkillsReadsTheProjectAndUserFoldersWithoutASession(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "user-skill")
	writeSkill(t, filepath.Join(project, ".claude", "skills"), "project-skill")

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Projects: skillsProjects{paths: map[domain.ProjectID]string{"proj-1": project}},
	}, httpd.ControlDeps{}))
	defer srv.Close()

	for _, tc := range []struct {
		name, query string
		want        []string
	}{
		{name: "project", query: "?projectId=proj-1", want: []string{"project-skill", "user-skill"}},
		// A project this host cannot resolve still lists the user's own skills.
		{name: "unknown project", query: "?projectId=gone", want: []string{"user-skill"}},
		{name: "no project", want: []string{"user-skill"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + "/api/v1/agents/claude-code/skills" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			var body controllers.ConversationSkillsResponse
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Skills) != len(tc.want) {
				t.Fatalf("skills = %+v, want %v", body.Skills, tc.want)
			}
			for i, name := range tc.want {
				if body.Skills[i].Name != name {
					t.Fatalf("skills = %+v, want %v", body.Skills, tc.want)
				}
			}
		})
	}
}
