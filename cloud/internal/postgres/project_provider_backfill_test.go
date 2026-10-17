package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The 00083 backfill pins each project without a recorded provider to the
// provider its latest session ran on, or to Coder when it carries Coder
// dev-kit config, and leaves everything else alone. It runs as a role subject
// to row-level security, as the migration role is in production, so it must
// work through the tenant context rather than around it.
func TestProjectSandboxProviderBackfill(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	migration, err := os.ReadFile("migrations/00083_project_sandbox_provider_backfill.sql")
	if err != nil {
		t.Fatal(err)
	}
	body := string(migration)
	body = body[strings.Index(body, "-- +goose StatementBegin")+len("-- +goose StatementBegin") : strings.Index(body, "-- +goose StatementEnd")]

	project := func(config string) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := admin.Exec(ctx, `INSERT INTO ao_projects (id, org_id, display_name, repository_url, config)
			VALUES ($1, $2, 'Backfill', $3, $4::jsonb)`, id, fixture.orgID, "https://example.test/"+id, config); err != nil {
			t.Fatal(err)
		}
		return id
	}
	session := func(projectID, provider string, created time.Time) {
		t.Helper()
		id := uuid.NewString()
		if _, err := admin.Exec(ctx, `INSERT INTO ao_sessions (id, org_id, project_id, kind, harness, display_name, branch, created_by_user_id)
			VALUES ($1, $2, $3, 'worker', 'codex', 'Session', 'main', $4)`, id, fixture.orgID, projectID, fixture.userID); err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, `INSERT INTO ao_sandboxes (session_id, org_id, provider, created_at) VALUES ($1, $2, $3, $4)`,
			id, fixture.orgID, provider, created); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	lastRanFreestyle := project(`{}`)
	session(lastRanFreestyle, "coder", now.Add(-2*time.Hour))
	session(lastRanFreestyle, "freestyle", now.Add(-time.Hour))
	coderConfigNeverRan := project(`{"coder":{"templateId":"tpl-1"}}`)
	recorded := project(`{"sandboxProvider":"coder"}`)
	session(recorded, "freestyle", now)
	neverRan := project(`{"source":"scratch"}`)
	// The fixture's own project ran only on docker, which is never pinned.

	role := "ao_backfill_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	for _, statement := range []string{
		`CREATE ROLE ` + role + ` NOLOGIN NOSUPERUSER NOBYPASSRLS`,
		`GRANT USAGE ON SCHEMA public TO ` + role,
		`GRANT SELECT, UPDATE ON ao_projects, ao_sessions, ao_sandboxes, ao_project_share_grants TO ` + role,
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP OWNED BY `+role)
		_, _ = admin.Exec(context.Background(), `DROP ROLE `+role)
	})
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+role); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, body); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if _, err := tx.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
	}
	pinned := func(projectID string) string {
		t.Helper()
		var provider *string
		if err := tx.QueryRow(ctx, `SELECT config->>'sandboxProvider' FROM ao_projects WHERE id = $1`, projectID).Scan(&provider); err != nil {
			t.Fatal(err)
		}
		if provider == nil {
			return ""
		}
		return *provider
	}
	for _, tc := range []struct{ name, projectID, want string }{
		{"latest session's provider", lastRanFreestyle, "freestyle"},
		{"coder config that never ran", coderConfigNeverRan, "coder"},
		{"recorded provider kept", recorded, "coder"},
		{"never ran, no coder config", neverRan, ""},
		{"ran only on a provider that is not pinned", fixture.projectID, ""},
	} {
		if got := pinned(tc.projectID); got != tc.want {
			t.Errorf("%s: sandboxProvider = %q, want %q", tc.name, got, tc.want)
		}
	}
	var source string
	if err := tx.QueryRow(ctx, `SELECT config->>'source' FROM ao_projects WHERE id = $1`, neverRan).Scan(&source); err != nil || source != "scratch" {
		t.Fatalf("other config keys must be kept: source = %q, err = %v", source, err)
	}
}
