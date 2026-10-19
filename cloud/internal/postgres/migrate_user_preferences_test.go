package postgres

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
)

func TestUserSandboxPreferenceMigrationPreservesLocalPreferences(t *testing.T) {
	// This opt-in URL must name a disposable database, never a developer's
	// existing Cloud database. Each case additionally owns an isolated schema.
	databaseURL := os.Getenv("AO_CLOUD_MIGRATION_SCRATCH_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AO_CLOUD_MIGRATION_SCRATCH_DATABASE_URL is not set; skipping scratch PostgreSQL migration test")
	}
	migration, err := migrationFiles.ReadFile("migrations/00084_user_sandbox_preferences.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, existing := range []string{"fresh", "table", "table and policy"} {
		t.Run(existing, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			admin, err := sql.Open("pgx", databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = admin.Close() })
			schema := "ao_prefmig_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			quotedSchema := pgx.Identifier{schema}.Sanitize()
			if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
					t.Errorf("drop scratch migration schema: %v", err)
				}
			})
			parsed, err := url.Parse(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			query := parsed.Query()
			query.Set("options", "-csearch_path="+schema)
			parsed.RawQuery = query.Encode()
			db, err := sql.Open("pgx", parsed.String())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, statement := range []string{
				`CREATE TABLE ao_users (id UUID PRIMARY KEY)`,
				`CREATE FUNCTION ao_current_user_id() RETURNS UUID LANGUAGE sql STABLE AS
				$$ SELECT NULLIF(current_setting('ao.user_id', true), '')::uuid $$`,
			} {
				if _, err := db.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			userID := uuid.NewString()
			if _, err := db.ExecContext(ctx, `INSERT INTO ao_users (id) VALUES ($1)`, userID); err != nil {
				t.Fatal(err)
			}
			var oldTable, oldPolicy uint32
			if existing != "fresh" {
				if _, err := db.ExecContext(ctx, `CREATE TABLE ao_user_sandbox_preferences (
					user_id UUID PRIMARY KEY REFERENCES ao_users(id) ON DELETE CASCADE,
					sandbox_provider TEXT, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
				)`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `INSERT INTO ao_user_sandbox_preferences
					(user_id, sandbox_provider, updated_at) VALUES ($1, 'coder', '2026-09-23T00:00:00Z')`, userID); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRowContext(ctx, `SELECT 'ao_user_sandbox_preferences'::regclass::oid`).Scan(&oldTable); err != nil {
					t.Fatal(err)
				}
				if existing == "table and policy" {
					for _, statement := range []string{
						`ALTER TABLE ao_user_sandbox_preferences ENABLE ROW LEVEL SECURITY`,
						`ALTER TABLE ao_user_sandbox_preferences FORCE ROW LEVEL SECURITY`,
						`CREATE POLICY ao_user_sandbox_preferences_owner_policy ON ao_user_sandbox_preferences
							USING (user_id = ao_current_user_id()) WITH CHECK (user_id = ao_current_user_id())`,
					} {
						if _, err := db.ExecContext(ctx, statement); err != nil {
							t.Fatal(err)
						}
					}
					if err := db.QueryRowContext(ctx, `SELECT oid FROM pg_policy
						WHERE polrelid = 'ao_user_sandbox_preferences'::regclass
						AND polname = 'ao_user_sandbox_preferences_owner_policy'`).Scan(&oldPolicy); err != nil {
						t.Fatal(err)
					}
				}
			}
			goose.SetBaseFS(fstest.MapFS{"migrations/00084_user_sandbox_preferences.sql": &fstest.MapFile{Data: migration}})
			t.Cleanup(func() { goose.SetBaseFS(migrationFiles) })
			if err := goose.SetDialect("postgres"); err != nil {
				t.Fatal(err)
			}
			if err := goose.UpContext(ctx, db, "migrations"); err != nil {
				t.Fatalf("apply preference migration: %v", err)
			}
			var table, policy uint32
			var enabled, forced bool
			var using, check string
			if err := db.QueryRowContext(ctx, `SELECT c.oid, c.relrowsecurity, c.relforcerowsecurity,
				p.oid, pg_get_expr(p.polqual, p.polrelid), pg_get_expr(p.polwithcheck, p.polrelid)
				FROM pg_class c JOIN pg_policy p ON p.polrelid = c.oid
				WHERE c.oid = 'ao_user_sandbox_preferences'::regclass
				AND p.polname = 'ao_user_sandbox_preferences_owner_policy'`).Scan(
				&table, &enabled, &forced, &policy, &using, &check,
			); err != nil {
				t.Fatal(err)
			}
			if !enabled || !forced || using != "(user_id = ao_current_user_id())" || check != using {
				t.Fatalf("preference RLS: enabled=%t forced=%t using=%q check=%q", enabled, forced, using, check)
			}
			if existing == "fresh" {
				return
			}
			if table != oldTable || (oldPolicy != 0 && policy != oldPolicy) {
				t.Fatalf("migration replaced existing objects: table %d -> %d, policy %d -> %d", oldTable, table, oldPolicy, policy)
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, `SELECT set_config('ao.user_id', $1, true)`, userID); err != nil {
				t.Fatal(err)
			}
			var provider string
			var updatedAt time.Time
			if err := tx.QueryRowContext(ctx, `SELECT sandbox_provider, updated_at
				FROM ao_user_sandbox_preferences WHERE user_id = $1`, userID).Scan(&provider, &updatedAt); err != nil {
				t.Fatal(err)
			}
			if provider != "coder" || !updatedAt.Equal(time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)) {
				t.Fatalf("existing preference changed: provider=%q updated_at=%s", provider, updatedAt)
			}
		})
	}
}
