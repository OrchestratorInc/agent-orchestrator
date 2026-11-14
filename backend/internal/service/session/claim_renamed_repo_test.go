package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeGitHubRepos struct {
	repos   map[string]ports.GitHubRepository
	err     error
	lookups []string
}

func (f *fakeGitHubRepos) ResolveGitHubRepository(_ context.Context, owner, name string) (ports.GitHubRepository, error) {
	key := strings.ToLower(owner + "/" + name)
	f.lookups = append(f.lookups, key)
	if f.err != nil {
		return ports.GitHubRepository{}, f.err
	}
	repo, ok := f.repos[key]
	if !ok {
		return ports.GitHubRepository{}, ports.ErrSCMNotFound
	}
	return repo, nil
}

func TestClaimPRFollowsGitHubRename(t *testing.T) {
	renamed := map[string]ports.GitHubRepository{
		// GitHub resolves the old origin path to the renamed repository.
		"untrivial-ai/agent-orchestrator":    {ID: 42, FullName: "OrchestratorInc/agent-orchestrator"},
		"orchestratorinc/agent-orchestrator": {ID: 42, FullName: "OrchestratorInc/agent-orchestrator"},
		"orchestratorinc/other":              {ID: 99, FullName: "OrchestratorInc/other"},
	}
	for _, tc := range []struct {
		name, origin, ref string
		repos             *fakeGitHubRepos
		wantErr           error
		wantLookups       int
	}{
		{"renamed origin, same repo id", "https://github.com/Untrivial-ai/agent-orchestrator.git", "https://github.com/OrchestratorInc/agent-orchestrator/pull/7", &fakeGitHubRepos{repos: renamed}, nil, 2},
		{"ssh origin, same repo id", "git@github.com:Untrivial-ai/agent-orchestrator.git", "https://github.com/OrchestratorInc/agent-orchestrator/pull/7", &fakeGitHubRepos{repos: renamed}, nil, 2},
		{"same new owner, different repo id", "https://github.com/Untrivial-ai/agent-orchestrator.git", "https://github.com/OrchestratorInc/other/pull/7", &fakeGitHubRepos{repos: renamed}, ErrProjectMismatch, 2},
		{"unknown PR repository", "https://github.com/Untrivial-ai/agent-orchestrator.git", "https://github.com/evil/agent-orchestrator/pull/7", &fakeGitHubRepos{repos: renamed}, ErrProjectMismatch, 2},
		{"lookup failure keeps mismatch", "https://github.com/Untrivial-ai/agent-orchestrator.git", "https://github.com/OrchestratorInc/agent-orchestrator/pull/7", &fakeGitHubRepos{err: errors.New("offline")}, ErrProjectMismatch, 1},
		{"exact origin needs no lookup", "https://github.com/OrchestratorInc/agent-orchestrator.git", "https://github.com/OrchestratorInc/agent-orchestrator/pull/7", &fakeGitHubRepos{repos: renamed}, nil, 0},
		{"non-github origin is never looked up", "https://git.internal.example/acme/repo.git", "https://github.com/OrchestratorInc/agent-orchestrator/pull/7", &fakeGitHubRepos{repos: renamed}, ErrProjectMismatch, 0},
		{"github enterprise PR is never looked up", "https://github.com/Untrivial-ai/agent-orchestrator.git", "https://acme.ghe.io/OrchestratorInc/agent-orchestrator/pull/7", &fakeGitHubRepos{repos: renamed}, ErrProjectMismatch, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
			st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: tc.origin}
			scm := &claimTargetSCM{}
			claimer := &fakePRClaimer{}
			svc := NewWithDeps(Deps{Store: st, SCM: scm, PRClaimer: claimer})
			svc.SetGitHubRepositoryResolver(tc.repos)

			_, err := svc.ClaimPR(context.Background(), "demo-1", tc.ref, ClaimPROptions{})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if len(tc.repos.lookups) != tc.wantLookups {
				t.Fatalf("lookups = %v, want %d", tc.repos.lookups, tc.wantLookups)
			}
			if tc.wantErr != nil {
				if len(scm.refs) != 0 || claimer.called {
					t.Fatal("rejected claim reached SCM or storage")
				}
				return
			}
			if !claimer.called || claimer.gotPR.URL != tc.ref {
				t.Fatalf("persisted claim = %+v", claimer.gotPR)
			}
		})
	}
}
