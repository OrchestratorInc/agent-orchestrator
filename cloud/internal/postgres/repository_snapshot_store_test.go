package postgres

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func TestRepositorySnapshotStoreUseReplaceAndCollect(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	key := "github:" + uuid.NewString() // unique per run; the test database is shared
	if _, ok, err := store.UseRepositorySandboxSnapshot(ctx, fixture.orgID, key, "freestyle", "codex"); err != nil || ok {
		t.Fatalf("lookup before any build: ok=%v err=%v", ok, err)
	}
	first := domain.RepositorySandboxSnapshot{
		OrgID: fixture.orgID, RepositoryKey: key, RepositoryIdentity: "acme/repo",
		Provider: "freestyle", Harness: "codex", SnapshotID: "sh-1", BaseSnapshotID: "sh-base",
	}
	if previous, err := store.ReplaceRepositorySandboxSnapshot(ctx, first); err != nil || previous != "" {
		t.Fatalf("first replace: previous=%q err=%v", previous, err)
	}
	second := first
	second.SnapshotID = "sh-2"
	if previous, err := store.ReplaceRepositorySandboxSnapshot(ctx, second); err != nil || previous != "sh-1" {
		t.Fatalf("second replace: previous=%q err=%v, want sh-1", previous, err)
	}
	got, ok, err := store.UseRepositorySandboxSnapshot(ctx, fixture.orgID, key, "freestyle", "codex")
	if err != nil || !ok || got.SnapshotID != "sh-2" || got.RepositoryIdentity != "acme/repo" {
		t.Fatalf("lookup = %+v ok=%v err=%v", got, ok, err)
	}

	// Just used, so a week-old cutoff leaves it alone.
	idle, err := store.DeleteIdleRepositorySandboxSnapshots(ctx, "freestyle", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range idle {
		if id == "sh-2" {
			t.Fatal("collected a snapshot used moments ago")
		}
	}
	// Past its cutoff it is collected once, across organizations, and gone.
	idle, err = store.DeleteIdleRepositorySandboxSnapshots(ctx, "freestyle", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(idle, "sh-2") {
		t.Fatalf("idle = %v, want sh-2", idle)
	}
	if _, ok, err := store.UseRepositorySandboxSnapshot(ctx, fixture.orgID, key, "freestyle", "codex"); err != nil || ok {
		t.Fatalf("lookup after collection: ok=%v err=%v", ok, err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// The migration runs as the schema owner, which forced RLS applies to like any
// other role; the carry-over must still see and copy every organization's
// project snapshots, keyed by repository.
func TestRepositorySnapshotMigrationCarriesSnapshotsUnderForcedRLS(t *testing.T) {
	databaseURL := os.Getenv("AO_CLOUD_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AO_CLOUD_TEST_DATABASE_URL is not set; skipping PostgreSQL migration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schema, role := "ao_snapmig_"+suffix, "ao_snapmig_"+suffix
	quotedSchema, quotedRole := pgx.Identifier{schema}.Sanitize(), pgx.Identifier{role}.Sanitize()
	for _, statement := range []string{
		"CREATE EXTENSION IF NOT EXISTS pgcrypto",
		"CREATE ROLE " + quotedRole + " LOGIN PASSWORD 'migrator' NOSUPERUSER NOBYPASSRLS",
		"CREATE SCHEMA " + quotedSchema + " AUTHORIZATION " + quotedRole,
	} {
		if _, err := admin.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
		_, _ = admin.ExecContext(context.Background(), "DROP ROLE "+quotedRole)
	})

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(role, "migrator")
	query := parsed.Query()
	query.Set("options", "-csearch_path="+schema+",public")
	parsed.RawQuery = query.Encode()
	migrator, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrator.Close() })
	goose.SetBaseFS(migrationFiles)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, migrator, "migrations", 75); err != nil {
		t.Fatalf("migrate to 00075: %v", err)
	}

	// Seed as superuser, skipping the grant trigger a repository id needs.
	seed, err := admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	orgA, orgB := uuid.NewString(), uuid.NewString()
	projectA, projectB, projectOld := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, statement := range []string{
		"SET search_path = " + quotedSchema + ", public",
		"SET session_replication_role = replica",
		`INSERT INTO ao_organizations (id, auth_provider, slug, display_name, kind) VALUES
			('` + orgA + `', 'local', 'a-` + suffix + `', 'A', 'team'),
			('` + orgB + `', 'local', 'b-` + suffix + `', 'B', 'team')`,
		`INSERT INTO ao_projects (id, org_id, display_name, repository_url, github_repository_id, github_repository_grant_id) VALUES
			('` + projectA + `', '` + orgA + `', 'A', 'https://github.com/Acme/Repo.git', 4242, gen_random_uuid()),
			('` + projectOld + `', '` + orgA + `', 'Old', 'https://github.com/acme/repo', 4242, gen_random_uuid()),
			('` + projectB + `', '` + orgB + `', 'B', 'https://github.com/other/tool', NULL, NULL)`,
		`INSERT INTO ao_project_sandbox_snapshots (org_id, project_id, provider, harness, snapshot_id, base_snapshot_id, created_at) VALUES
			('` + orgA + `', '` + projectA + `', 'freestyle', 'claude-code', 'sh-new', 'sh-base', now()),
			('` + orgA + `', '` + projectOld + `', 'freestyle', 'claude-code', 'sh-old', 'sh-base', now() - interval '1 day'),
			('` + orgB + `', '` + projectB + `', 'freestyle', 'codex', 'sh-b', 'sh-base', now())`,
		"SET session_replication_role = origin",
	} {
		if _, err := seed.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	if err := goose.UpToContext(ctx, migrator, "migrations", 76); err != nil {
		t.Fatalf("migrate to 00076: %v", err)
	}
	rows, err := seed.QueryContext(ctx, `SELECT org_id::text, repository_key, repository_identity, harness, snapshot_id
		FROM ao_repository_sandbox_snapshots ORDER BY snapshot_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var org, key, identity, harness, snapshot string
		if err := rows.Scan(&org, &key, &identity, &harness, &snapshot); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{org, key, identity, harness, snapshot}, " "))
	}
	want := []string{
		orgB + " url:other/tool other/tool codex sh-b",
		orgA + " github:4242 acme/repo claude-code sh-new",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("carried snapshots:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var forced int
	if err := seed.QueryRowContext(ctx, `SELECT count(*) FROM pg_class
		WHERE relnamespace = $1::regnamespace
		  AND relname IN ('ao_projects', 'ao_repository_sandbox_snapshots')
		  AND relforcerowsecurity`, schema).Scan(&forced); err != nil {
		t.Fatal(err)
	}
	if forced != 2 {
		t.Fatalf("forced RLS restored on %d of 2 tables", forced)
	}
	if err := goose.DownToContext(ctx, migrator, "migrations", 75); err != nil {
		t.Fatalf("migrate down to 00075: %v", err)
	}
}
