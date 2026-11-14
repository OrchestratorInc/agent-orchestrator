package sqlite

import (
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

// A pre-release build applied the storage migration as 20261113130000000
// before it was renumbered. Such a database already holds the tables; the
// renumbered file must apply over them and keep the user's settings.
func TestMigrateAppliesRenumberedStorageManagementOverItsEarlierVersion(t *testing.T) {
	const (
		earlierVersion = int64(20261113130000000)
		current        = "migrations/20261114133306000_storage_management.sql"
	)
	db := openMigratedDatabaseCopy(t, 20261114072016988)

	contents, err := migrationsFS.ReadFile(current)
	if err != nil {
		t.Fatalf("read storage migration: %v", err)
	}
	gooseMu.Lock()
	goose.SetBaseFS(fstest.MapFS{
		"migrations/20261113130000000_storage_management.sql": &fstest.MapFile{Data: contents},
	})
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		gooseMu.Unlock()
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(db, "migrations", goose.WithAllowMissing()); err != nil {
		gooseMu.Unlock()
		t.Fatalf("apply storage migration under its earlier version: %v", err)
	}
	gooseMu.Unlock()
	if _, err := db.Exec(`UPDATE storage_settings SET backup_days = 30 WHERE id = 1`); err != nil {
		t.Fatalf("change a storage setting: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate a database holding the earlier storage version: %v", err)
	}

	var rows, backupDays int
	if err := db.QueryRow(`SELECT COUNT(*), MAX(backup_days) FROM storage_settings`).Scan(&rows, &backupDays); err != nil {
		t.Fatalf("read storage settings: %v", err)
	}
	if rows != 1 || backupDays != 30 {
		t.Fatalf("storage_settings rows=%d backup_days=%d, want 1 row keeping 30", rows, backupDays)
	}
	for _, version := range []int64{earlierVersion, 20261114133306000} {
		var applied int
		if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, version).Scan(&applied); err != nil {
			t.Fatalf("read migration %d: %v", version, err)
		}
		if applied != 1 {
			t.Fatalf("migration %d applied = %d, want 1", version, applied)
		}
	}
}
