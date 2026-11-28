package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const dockerSectionHeading = "## Docker Containers Started By This Session"

func TestSystemPromptDockerSectionFollowsDockerAvailability(t *testing.T) {
	project := promptProject{ID: "mer", Repo: "https://github.com/acme/mercury", DefaultBranch: "main"}
	for _, tc := range []struct {
		name       string
		standalone bool
	}{
		{"project worker", false},
		{"standalone worker", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := systemPromptConfig{Role: sessionPromptRoleWorker, Standalone: tc.standalone, Project: project}
			without := buildSystemPromptText(cfg)
			if strings.Contains(without, dockerSectionHeading) || strings.Contains(without, "--label ao.session=") {
				t.Fatalf("a host without Docker still gets the container label rules:\n%s", without)
			}
			cfg.Docker = true
			with := buildSystemPromptText(cfg)
			if !strings.Contains(with, dockerSectionHeading) {
				t.Fatalf("a host with Docker is missing the container label rules:\n%s", with)
			}
		})
	}
}

func TestSystemPromptDockerSectionNeverReachesOrchestrator(t *testing.T) {
	got := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleOrchestrator, Project: promptProject{ID: "mer"}, Docker: true})
	if strings.Contains(got, dockerSectionHeading) {
		t.Fatalf("orchestrator prompt carries the worker container rules:\n%s", got)
	}
}

func TestBuildSystemPromptReadsDockerFromTheHost(t *testing.T) {
	ctx := t.Context()
	m, _, _, _ := newManager()
	for _, available := range []bool{false, true} {
		m.dockerAvailable = func() bool { return available }
		sp, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
		if err != nil {
			t.Fatalf("buildSystemPrompt(docker=%v): %v", available, err)
		}
		if got := strings.Contains(sp, dockerSectionHeading); got != available {
			t.Fatalf("docker available = %v but prompt has the section = %v", available, got)
		}
	}
}
