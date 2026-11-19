package sqlite

import (
	"testing"

	"github.com/pressly/goose/v3"
)

// The stale-binary columns and their change trigger apply and roll back
// cleanly, and existing sessions read as current after the upgrade.
func TestMigrateSessionStaleBinaryRoundTrip(t *testing.T) {
	db := openMigratedDatabaseCopyAt(t, t.TempDir(), 212, pragmas)
	for _, query := range []string{
		`INSERT INTO projects (id, path, registered_at) VALUES ('project', '/tmp/project', CURRENT_TIMESTAMP)`,
		`INSERT INTO sessions (id, project_id, num, activity_last_at, created_at, updated_at)
		 VALUES ('session', 'project', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(migrationsFS)
	defer goose.SetBaseFS(nil)
	if err := goose.UpTo(db, "migrations", 214); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	var since any
	var version, launch string
	if err := db.QueryRow(`SELECT stale_binary_since, stale_binary_version, stale_binary_launch FROM sessions WHERE id = 'session'`).Scan(&since, &version, &launch); err != nil {
		t.Fatal(err)
	}
	if since != nil || version != "" || launch != "" {
		t.Fatalf("existing session reads stale: %v %q %q", since, version, launch)
	}
	var refresh bool
	if err := db.QueryRow(`SELECT version_refresh FROM app_settings WHERE id = 1`).Scan(&refresh); err != nil || !refresh {
		t.Fatalf("version_refresh = %t, %v", refresh, err)
	}
	if _, err := db.Exec(`UPDATE sessions SET stale_binary_since = CURRENT_TIMESTAMP WHERE id = 'session'`); err != nil {
		t.Fatal(err)
	}
	var changes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM change_log WHERE session_id = 'session' AND event_type = 'session_updated'`).Scan(&changes); err != nil || changes != 1 {
		t.Fatalf("change events = %d, %v", changes, err)
	}
	if err := goose.DownTo(db, "migrations", 212); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if _, err := db.Exec(`SELECT stale_binary_since FROM sessions`); err == nil {
		t.Fatal("stale_binary_since survived the rollback")
	}
	if err := goose.UpTo(db, "migrations", 214); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}
