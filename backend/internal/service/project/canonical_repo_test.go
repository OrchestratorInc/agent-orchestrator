package project_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type fakeGitHubRepos struct {
	mu      sync.Mutex
	repos   map[string]ports.GitHubRepository
	err     error
	lookups []string
}

func (f *fakeGitHubRepos) ResolveGitHubRepository(_ context.Context, owner, name string) (ports.GitHubRepository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, owner+"/"+name)
	if f.err != nil {
		return ports.GitHubRepository{}, f.err
	}
	return f.repos[owner+"/"+name], nil
}

const orchestratorIncAvatar = "https://avatars.githubusercontent.com/u/310302959?v=4"

func TestManager_GetReportsCanonicalGitHubRepo(t *testing.T) {
	renamed := map[string]ports.GitHubRepository{
		"Untrivial-ai/agent-orchestrator": {ID: 42, FullName: "OrchestratorInc/agent-orchestrator", OwnerAvatarURL: orchestratorIncAvatar},
		"acme/widgets":                    {ID: 7, FullName: "acme/widgets"},
	}
	for _, tc := range []struct {
		name        string
		origin      string
		resolver    *fakeGitHubRepos
		want        string
		wantAvatar  string
		wantLookups int
	}{
		{name: "renamed org", origin: "https://github.com/Untrivial-ai/agent-orchestrator.git", resolver: &fakeGitHubRepos{repos: renamed}, want: "OrchestratorInc/agent-orchestrator", wantAvatar: orchestratorIncAvatar, wantLookups: 1},
		{name: "ssh remote", origin: "git@github.com:Untrivial-ai/agent-orchestrator.git", resolver: &fakeGitHubRepos{repos: renamed}, want: "OrchestratorInc/agent-orchestrator", wantAvatar: orchestratorIncAvatar, wantLookups: 1},
		{name: "unchanged", origin: "https://github.com/acme/widgets.git", resolver: &fakeGitHubRepos{repos: renamed}, want: "acme/widgets", wantLookups: 1},
		{name: "api failure falls back", origin: "https://github.com/acme/widgets.git", resolver: &fakeGitHubRepos{err: errors.New("offline")}, want: "", wantLookups: 1},
		{name: "self-hosted host is never looked up", origin: "https://git.internal.example/acme/widgets.git", resolver: &fakeGitHubRepos{repos: renamed}, want: "", wantLookups: 0},
		{name: "gitlab is never looked up", origin: "https://gitlab.com/acme/widgets.git", resolver: &fakeGitHubRepos{repos: renamed}, want: "", wantLookups: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
			store, err := sqlitetest.Open(t.TempDir())
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			m := project.NewWithDeps(project.Deps{Store: store, GitHubRepos: tc.resolver})
			ctx := context.Background()
			if _, err := m.Add(ctx, project.AddInput{Path: gitRepoWithOrigin(t, tc.origin), ProjectID: ptr("p")}); err != nil {
				t.Fatalf("Add: %v", err)
			}

			got, err := m.Get(ctx, "p")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Project.Repo != tc.origin {
				t.Fatalf("Repo = %q, want the untouched origin %q", got.Project.Repo, tc.origin)
			}
			if got.Project.CanonicalRepo != tc.want {
				t.Fatalf("CanonicalRepo = %q, want %q", got.Project.CanonicalRepo, tc.want)
			}
			if got.Project.CanonicalOwnerAvatarURL != tc.wantAvatar {
				t.Fatalf("CanonicalOwnerAvatarURL = %q, want %q", got.Project.CanonicalOwnerAvatarURL, tc.wantAvatar)
			}
			if len(tc.resolver.lookups) != tc.wantLookups {
				t.Fatalf("lookups = %v, want %d", tc.resolver.lookups, tc.wantLookups)
			}
		})
	}
}

func TestManager_WarmCanonicalReposLooksUpOnlyGitHubOrigins(t *testing.T) {
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	resolver := &fakeGitHubRepos{repos: map[string]ports.GitHubRepository{}}
	m := project.NewWithDeps(project.Deps{Store: store, GitHubRepos: resolver})
	ctx := context.Background()
	for id, origin := range map[string]string{
		"gh":       "https://github.com/Untrivial-ai/agent-orchestrator.git",
		"internal": "https://git.internal.example/acme/widgets.git",
	} {
		if _, err := m.Add(ctx, project.AddInput{Path: gitRepoWithOrigin(t, origin), ProjectID: ptr(id)}); err != nil {
			t.Fatalf("Add %s: %v", id, err)
		}
	}

	m.WarmCanonicalRepos(ctx)

	if len(resolver.lookups) != 1 || resolver.lookups[0] != "Untrivial-ai/agent-orchestrator" {
		t.Fatalf("lookups = %v, want only the github.com origin", resolver.lookups)
	}
}
