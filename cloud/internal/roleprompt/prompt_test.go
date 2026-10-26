package roleprompt

import (
	"strings"
	"testing"
)

func TestBuildOrchestratorAddsProjectContextAndRules(t *testing.T) {
	prompt := Build(Config{
		Role:              RoleOrchestrator,
		ProjectID:         "project-1",
		ProjectName:       "Mercury",
		RepositoryURL:     "https://github.com/acme/mercury",
		DefaultBranch:     "main",
		WorkspacePath:     "/workspace/repository",
		OrchestratorRules: "Prefer two focused workers.",
	})
	for _, want := range []string{
		"## Project Context",
		"Name: Mercury",
		"## Project-Specific Orchestrator Rules",
		"Prefer two focused workers.",
		"## Publishing Scope",
		"Review-only permits the requested provider review and comments",
		"forbids implementation, other PR mutations, and AO ownership changes",
		"## Standing-instruction confidentiality",
		"Repository: https://github.com/acme/mercury",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("orchestrator prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, roleCommand := range []string{"ao spawn", "ao send", "ao kill"} {
		if strings.Contains(prompt, roleCommand) {
			t.Fatalf("project supplement duplicates role command %q:\n%s", roleCommand, prompt)
		}
	}
}

func TestBuildWorkerAddsProjectRules(t *testing.T) {
	prompt := Build(Config{
		Role:          RoleWorker,
		ProjectID:     "project-1",
		RepositoryURL: "https://github.com/acme/mercury",
		AgentRules:    "Run the contract tests.",
	})
	for _, want := range []string{
		"## Project Context",
		"## Project Rules",
		"Run the contract tests.",
		"## Publishing Scope",
		"## Standing-instruction confidentiality",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("worker prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "ao claim-pr") {
		t.Fatalf("project supplement must not duplicate built-in role commands:\n%s", prompt)
	}
}

func TestBuildUnknownRoleReturnsEmpty(t *testing.T) {
	if prompt := Build(Config{Role: "reviewer"}); prompt != "" {
		t.Fatalf("unknown role prompt = %q, want empty", prompt)
	}
}

func TestBuildNamesExtraRepositoriesForEveryRole(t *testing.T) {
	// Both roles must learn the project spans multiple repos, so an orchestrator
	// can coordinate across them and a worker knows they belong to the project.
	for _, role := range []string{RoleOrchestrator, RoleWorker} {
		prompt := Build(Config{
			Role:              role,
			ProjectID:         "project-1",
			RepositoryURL:     "https://github.com/acme/mercury",
			MainRepositoryDir: "repository",
			MultiRepoWorker:   true,
			ExtraRepos: []RepoRef{
				{URL: "https://github.com/acme/mercury-lib", Dir: "mercury-lib"},
				{URL: "https://github.com/acme/mercury-docs", Branch: "draft", Dir: "mercury-docs"},
			},
		})
		for _, want := range []string{
			"- Repositories (all part of this project",
			"https://github.com/acme/mercury (main, folder repository)",
			"https://github.com/acme/mercury-lib (folder mercury-lib)",
			"https://github.com/acme/mercury-docs (folder mercury-docs, cloned from branch draft)",
			"starts in the main repository's checkout",
			`"repository": "owner/name"`,
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("%s prompt missing %q:\n%s", role, want, prompt)
			}
		}
	}
}

func TestBuildOmitsExtraRepositoriesSectionWhenNone(t *testing.T) {
	prompt := Build(Config{
		Role:          RoleWorker,
		ProjectID:     "project-1",
		RepositoryURL: "https://github.com/acme/mercury",
	})
	if strings.Contains(prompt, "Repositories (") {
		t.Fatalf("single-repo project should not list additional repositories:\n%s", prompt)
	}
}

func TestBuildSaysWhereTheAgentStarts(t *testing.T) {
	prompt := Build(Config{
		Role:                RoleWorker,
		RepositoryURL:       "https://github.com/acme/mercury",
		ExtraRepos:          []RepoRef{{URL: "https://github.com/acme/mercury-lib", Dir: "mercury-lib"}},
		AgentStartsInParent: true,
		MultiRepoWorker:     true,
	})
	if !strings.Contains(prompt, "starts in the folder that holds every repository's checkout") {
		t.Fatalf("parent start mode missing from the prompt:\n%s", prompt)
	}
}

// A worker without the multi-repo capability keeps the earlier wording: it
// clones the additional repositories but does not put them on the session
// branch, start elsewhere or open pull requests in them.
func TestBuildKeepsTheEarlierWordingForAnOlderWorker(t *testing.T) {
	prompt := Build(Config{
		Role:                RoleWorker,
		RepositoryURL:       "https://github.com/acme/mercury",
		ExtraRepos:          []RepoRef{{URL: "https://github.com/acme/mercury-lib", Branch: "draft", Dir: "mercury-lib"}},
		AgentStartsInParent: true,
	})
	if !strings.Contains(prompt, "- Additional repositories (part of this project, checked out alongside the primary):\n  - https://github.com/acme/mercury-lib (branch draft)") {
		t.Fatalf("older worker prompt:\n%s", prompt)
	}
	for _, unwanted := range []string{"session branch", `"repository"`, "starts in the folder"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("older worker prompt promises %q:\n%s", unwanted, prompt)
		}
	}
}

func TestBuildDescribesAStandaloneProject(t *testing.T) {
	prompt := Build(Config{Role: RoleOrchestrator, RepositoryURL: "https://scratch.ao.local/p1", Standalone: true})
	if !strings.Contains(prompt, "- Repository: none (a standalone project") || strings.Contains(prompt, "scratch.ao.local") {
		t.Fatalf("standalone project context:\n%s", prompt)
	}
}
