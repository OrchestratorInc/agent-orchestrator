package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Only a repository an organization tracks (here: a project on it) admits SCM
// webhooks; every other repository the installation can see is dropped.
func TestGitHubRepositoryTrackedRequiresATrackingOrganization(t *testing.T) {
	store, admin := openGitHubMultiOrgStore(t)
	ctx := context.Background()
	githubInstallationID := randomGitHubInstallationID(t)
	fixture := seedGitHubOrg(t, admin, githubInstallationID)
	installation, err := store.CompleteGitHubInstallation(ctx, fixture.stateHash, testGitHubInstallation(githubInstallationID))
	if err != nil {
		t.Fatalf("connect installation: %v", err)
	}
	trackedRepositoryID := randomGitHubInstallationID(t)
	untrackedRepositoryID := randomGitHubInstallationID(t)

	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, fixture.orgID); err != nil {
		t.Fatal(err)
	}
	grantID := uuid.NewString()
	for _, step := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO ao_github_repositories (github_repository_id, github_owner_account_id, name, full_name, html_url, clone_url, visibility)
			VALUES ($1, 1, 'tracked', 'acme/tracked', 'https://github.com/acme/tracked', 'https://github.com/acme/tracked.git', 'public'),
			       ($2, 1, 'other', 'acme/other', 'https://github.com/acme/other', 'https://github.com/acme/other.git', 'public')`,
			[]any{trackedRepositoryID, untrackedRepositoryID}},
		{`INSERT INTO ao_github_repository_grants (id, org_id, installation_id, github_repository_id, repository_selection)
			VALUES ($1, $2, $3, $4, 'all')`,
			[]any{grantID, fixture.orgID, installation.ID, trackedRepositoryID}},
		{`INSERT INTO ao_projects (org_id, display_name, repository_url, github_repository_id, github_repository_grant_id)
			VALUES ($1, 'tracked', 'https://github.com/acme/tracked', $2, $3)`,
			[]any{fixture.orgID, trackedRepositoryID, grantID}},
	} {
		if _, err := tx.Exec(ctx, step.query, step.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if tracked, err := store.GitHubRepositoryTracked(ctx, githubInstallationID, trackedRepositoryID); err != nil || !tracked {
		t.Fatalf("project repository: tracked=%v err=%v; want tracked", tracked, err)
	}
	if tracked, err := store.GitHubRepositoryTracked(ctx, githubInstallationID, untrackedRepositoryID); err != nil || tracked {
		t.Fatalf("repository without a project: tracked=%v err=%v; want untracked", tracked, err)
	}
	if tracked, err := store.GitHubRepositoryTracked(ctx, randomGitHubInstallationID(t), trackedRepositoryID); err != nil || tracked {
		t.Fatalf("unrouted installation: tracked=%v err=%v; want untracked", tracked, err)
	}
}
