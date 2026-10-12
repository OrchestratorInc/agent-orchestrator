package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func wholeInstallation(installation domain.GitHubInstallation) domain.GitHubInstallation {
	installation.AccessScope = domain.GitHubAccessScopeInstallation
	return installation
}

// addOrgMember adds another active member to the organization, under its
// org context as row-level security requires.
func addOrgMember(t *testing.T, admin *pgxpool.Pool, orgID string) string {
	t.Helper()
	ctx := context.Background()
	member := uuid.NewString()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ao_users (id, auth_provider, external_user_id, email, display_name, password_hash)
		VALUES ($1::uuid, 'local', $1::uuid::text, $1::uuid::text || '@example.test', 'Member', 'hash')`, member); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ao_org_memberships (org_id, user_id, role) VALUES ($1, $2, 'member')`, orgID, member); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id = $1`, member) })
	return member
}

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

	// A connect that did not decide a scope (no administration check) never
	// widens a user-scoped connection; only an explicit whole one does.
	unspecified, err := store.BindGitHubInstallation(ctx, principal, org.orgID, testGitHubInstallation(githubInstallationID))
	if err != nil || !unspecified.UserScoped() || unspecified.ScopeUserID != org.userID {
		t.Fatalf("unspecified connect over a user-scoped one = %+v, err = %v; want it kept user-scoped", unspecified, err)
	}
	whole, err := store.BindGitHubInstallation(ctx, principal, org.orgID, wholeInstallation(testGitHubInstallation(githubInstallationID)))
	if err != nil || whole.UserScoped() || whole.ScopeUserID != "" {
		t.Fatalf("whole connect = %+v, err = %v", whole, err)
	}

	// An owner's whole-installation connection is not narrowed by a later
	// user-scoped connect into the same organization.
	again, err := store.BindGitHubInstallation(ctx, principal, org.orgID,
		userScoped(testGitHubInstallation(githubInstallationID), org.userID))
	if err != nil || again.UserScoped() {
		t.Fatalf("user-scoped connect over a whole one = %+v, err = %v; want it kept whole", again, err)
	}

	// With a second member the organization is no longer personal.
	addOrgMember(t, admin, org.orgID)
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

// A user-scoped connection's grants are honoured only while its user is the
// organization's only active member: once someone joins, the repositories
// disappear from the listing and from every grant lookup.
func TestUserScopedGrantsEndWhenTheOrganizationGainsAMember(t *testing.T) {
	store, admin := openGitHubMultiOrgStore(t)
	ctx := context.Background()
	githubInstallationID := randomGitHubInstallationID(t)
	org := seedGitHubOrg(t, admin, githubInstallationID)
	principal := domain.Principal{UserID: org.userID, Provider: "local"}
	bound, err := store.BindGitHubInstallation(ctx, principal, org.orgID,
		userScoped(testGitHubInstallation(githubInstallationID), org.userID))
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.BeginGitHubRepositorySync(ctx, bound)
	if err != nil {
		t.Fatal(err)
	}
	repositoryID := randomGitHubInstallationID(t)
	fullName := "octo-org/scoped-" + uuid.NewString()[:8]
	now := time.Now().UTC()
	if err := store.ReconcileGitHubRepositories(ctx, org.orgID, bound, generation, []domain.GitHubRepository{{
		GitHubRepositoryID: repositoryID, GitHubOwnerID: 7, Name: "scoped", FullName: fullName,
		HTMLURL: "https://github.com/" + fullName, CloneURL: "https://github.com/" + fullName + ".git",
		DefaultBranch: "main", Visibility: "private", IsPrivate: true, GitHubUpdatedAt: &now,
	}}); err != nil {
		t.Fatal(err)
	}
	visible := func() int {
		repositories, _, err := store.ListGitHubRepositories(ctx, principal, org.orgID, nil, 50)
		if err != nil {
			t.Fatal(err)
		}
		return len(repositories)
	}
	if got := visible(); got != 1 {
		t.Fatalf("listed %d repositories, want the granted one", got)
	}
	if intact, err := store.GitHubUserScopeIntact(ctx, org.orgID, org.userID); err != nil || !intact {
		t.Fatalf("intact = %v, err = %v; want true for the only member", intact, err)
	}
	if scope, _, err := store.GitHubRepositoryAccessScope(ctx, org.orgID, fullName); err != nil || scope != domain.GitHubAccessScopeUser {
		t.Fatalf("scope = %q, err = %v", scope, err)
	}

	addOrgMember(t, admin, org.orgID)

	if got := visible(); got != 0 {
		t.Fatalf("listed %d repositories after another member joined, want none", got)
	}
	if intact, err := store.GitHubUserScopeIntact(ctx, org.orgID, org.userID); err != nil || intact {
		t.Fatalf("intact = %v, err = %v; want false once the organization is shared", intact, err)
	}
	if _, _, err := store.GitHubRepositoryAccessScope(ctx, org.orgID, fullName); !errors.Is(err, ErrNotFound) {
		t.Fatalf("access scope lookup: err = %v, want ErrNotFound", err)
	}
	if _, _, err := store.GitHubInstallationForRepository(ctx, org.orgID, fullName); !errors.Is(err, ErrNotFound) {
		t.Fatalf("installation lookup: err = %v, want ErrNotFound", err)
	}
	if _, onlyMember, err := store.GitHubConnectOthersTarget(ctx, org.orgID, org.userID); err != nil || onlyMember {
		t.Fatalf("connect-others target: onlyMember = %v, err = %v", onlyMember, err)
	}
}

// The only-member check fails closed outside the organization's own context,
// where row-level security would hide the other members from it.
func TestGitHubUserScopeIntactNeedsTheOrganizationContext(t *testing.T) {
	_, admin := openGitHubMultiOrgStore(t)
	ctx := context.Background()
	org := seedGitHubOrg(t, admin, randomGitHubInstallationID(t))
	var intact bool
	if err := admin.QueryRow(ctx, `SELECT ao_github_user_scope_intact($1::uuid, $2::uuid)`,
		org.orgID, org.userID).Scan(&intact); err != nil {
		t.Fatal(err)
	}
	if intact {
		t.Fatal("intact without ao.org_id; want false")
	}
}
