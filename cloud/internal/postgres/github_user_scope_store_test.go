package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func userScoped(installation domain.GitHubInstallation, userID string) domain.GitHubInstallation {
	installation.AccessScope = domain.GitHubAccessScopeUser
	installation.ScopeUserID = userID
	return installation
}

// A user-scoped connection is stored with its scope and user, only into an
// organization whose only active member is the connecting user.
func TestBindUserScopedGitHubInstallation(t *testing.T) {
	store, admin := openGitHubMultiOrgStore(t)
	ctx := context.Background()
	githubInstallationID := randomGitHubInstallationID(t)
	org := seedGitHubOrg(t, admin, githubInstallationID)
	principal := domain.Principal{UserID: org.userID, Provider: "local"}

	if _, err := store.BindGitHubInstallation(ctx, principal, org.orgID,
		userScoped(testGitHubInstallation(githubInstallationID), uuid.NewString())); !errors.Is(err, ErrInvalid) {
		t.Fatalf("scope user other than the principal: err = %v, want ErrInvalid", err)
	}

	bound, err := store.BindGitHubInstallation(ctx, principal, org.orgID,
		userScoped(testGitHubInstallation(githubInstallationID), org.userID))
	if err != nil {
		t.Fatal(err)
	}
	if !bound.UserScoped() || bound.ScopeUserID != org.userID {
		t.Fatalf("bound = %+v, want user-scoped to %s", bound, org.userID)
	}
	listed, err := store.ListGitHubInstallations(ctx, principal, org.orgID)
	if err != nil || len(listed) != 1 || !listed[0].UserScoped() || listed[0].ScopeUserID != org.userID {
		t.Fatalf("listed = %+v, err = %v", listed, err)
	}
	scope, scopeUser, err := store.GitHubRepositoryAccessScope(ctx, org.orgID, "octo-org/none")
	if !errors.Is(err, ErrNotFound) || scope != "" || scopeUser != "" {
		t.Fatalf("scope of an ungranted repository: %q %q %v", scope, scopeUser, err)
	}

	// An owner's whole-installation connection is not narrowed by a later
	// user-scoped connect into the same organization.
	whole, err := store.BindGitHubInstallation(ctx, principal, org.orgID, testGitHubInstallation(githubInstallationID))
	if err != nil || whole.UserScoped() || whole.ScopeUserID != "" {
		t.Fatalf("whole connect = %+v, err = %v", whole, err)
	}
	again, err := store.BindGitHubInstallation(ctx, principal, org.orgID,
		userScoped(testGitHubInstallation(githubInstallationID), org.userID))
	if err != nil || again.UserScoped() {
		t.Fatalf("user-scoped connect over a whole one = %+v, err = %v; want it kept whole", again, err)
	}

	// With a second member the organization is no longer personal.
	member := uuid.NewString()
	if _, err := admin.Exec(ctx,
		`INSERT INTO ao_users (id, auth_provider, external_user_id, email, display_name, password_hash)
		VALUES ($1::uuid, 'local', $1::uuid::text, $1::uuid::text || '@example.test', 'Member', 'hash')`, member); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx,
		`INSERT INTO ao_org_memberships (org_id, user_id, role) VALUES ($1, $2, 'member')`, org.orgID, member); err != nil {
		t.Fatal(err)
	}
	other := randomGitHubInstallationID(t)
	if _, err := store.BindGitHubInstallation(ctx, principal, org.orgID,
		userScoped(testGitHubInstallation(other), org.userID)); !errors.Is(err, ErrGitHubUserScopeNeedsPersonalOrg) {
		t.Fatalf("team org: err = %v, want ErrGitHubUserScopeNeedsPersonalOrg", err)
	}
}

// The installation callback keeps the user's GitHub authorization without a
// user authorization attempt; saving again replaces it.
func TestSaveGitHubUserConnection(t *testing.T) {
	store, admin := openGitHubMultiOrgStore(t)
	ctx := context.Background()
	org := seedGitHubOrg(t, admin, randomGitHubInstallationID(t))
	githubUserID := randomGitHubInstallationID(t)
	input := GitHubUserConnectionInput{
		GitHubUserID: githubUserID, GitHubLogin: "member",
		AccessTokenCiphertext: []byte("ciphertext-1"), AccessTokenNonce: []byte("nonce-000001"),
	}
	if _, err := store.SaveGitHubUserConnection(ctx, org.userID, input); err != nil {
		t.Fatal(err)
	}
	input.AccessTokenCiphertext = []byte("ciphertext-2")
	saved, err := store.SaveGitHubUserConnection(ctx, org.userID, input)
	if err != nil {
		t.Fatal(err)
	}
	read, err := store.GitHubUserConnection(ctx, org.userID)
	if err != nil || read.GitHubUserID != githubUserID || string(read.AccessTokenCiphertext) != "ciphertext-2" ||
		saved.GitHubLogin != "member" {
		t.Fatalf("read = %+v, err = %v", read, err)
	}
	if _, err := store.SaveGitHubUserConnection(ctx, "", input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no user: err = %v, want ErrInvalid", err)
	}
}
