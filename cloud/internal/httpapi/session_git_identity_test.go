package httpapi

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

type gitIdentityStore struct {
	Store
	creator    string
	connection *domain.GitHubUserConnection
}

func (s *gitIdentityStore) SessionCreatorUserID(context.Context, string, string) (string, error) {
	if s.creator == "" {
		return "", postgres.ErrNotFound
	}
	return s.creator, nil
}

func (s *gitIdentityStore) GitHubUserConnection(_ context.Context, userID string) (domain.GitHubUserConnection, error) {
	if s.connection == nil || userID != s.creator {
		return domain.GitHubUserConnection{}, postgres.ErrNotFound
	}
	return *s.connection, nil
}

// A session's commits are authored as its owner, from the GitHub
// authorization they stored; without one the worker keeps AO's identity.
func TestApplySessionGitIdentityUsesTheOwnersGitHubIdentity(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner := &gitIdentityStore{creator: "user-owner", connection: &domain.GitHubUserConnection{
		UserID: "user-owner", GitHubUserID: 42, GitHubLogin: "octocat",
		AccessTokenCiphertext: []byte("ciphertext"),
	}}
	var launch worker.LaunchContext
	New(Options{Store: owner, Logger: logger}).applySessionGitIdentity(context.Background(), "org", "sess", &launch)
	if launch.GitAuthorName != "octocat" || launch.GitAuthorEmail != "42+octocat@users.noreply.github.com" {
		t.Fatalf("launch identity = %q <%q>, want the owner's GitHub identity", launch.GitAuthorName, launch.GitAuthorEmail)
	}

	for name, store := range map[string]*gitIdentityStore{
		"no creator":             {},
		"creator without GitHub": {creator: "user-owner"},
	} {
		var launch worker.LaunchContext
		New(Options{Store: store, Logger: logger}).applySessionGitIdentity(context.Background(), "org", "sess", &launch)
		if launch.GitAuthorName != "" || launch.GitAuthorEmail != "" {
			t.Fatalf("%s: launch identity = %q <%q>, want none", name, launch.GitAuthorName, launch.GitAuthorEmail)
		}
		if identity := launch.GitIdentity(); identity.Name != "AO Cloud Agent" {
			t.Fatalf("%s: worker identity = %+v, want AO's", name, identity)
		}
	}
}
