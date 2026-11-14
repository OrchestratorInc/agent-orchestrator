package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenPreMigratedSucceedsForMigratedDatabase(t *testing.T) {
	dataDir := t.TempDir()

	db := openMigratedTestDBIn(t, dataDir)
	if err := db.Close(); err != nil {
		t.Fatalf("close migrated db: %v", err)
	}

	store, err := OpenPreMigrated(dataDir)
	if err != nil {
		t.Fatalf("OpenPreMigrated: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
}

func TestOpenPreMigratedRejectsNewEmptyDatabase(t *testing.T) {
	dataDir := t.TempDir()
	if err := createEmptyDatabase(filepath.Join(dataDir, "ao.db")); err != nil {
		t.Fatalf("create empty database: %v", err)
	}

	_, err := OpenPreMigrated(dataDir)
	if err == nil {
		t.Fatal("OpenPreMigrated succeeded on empty database; want error")
	}
}

func TestOpenPreMigratedRejectsStaleMigrationVersion(t *testing.T) {
	dataDir := t.TempDir()

	want, err := expectedMigrationVersion()
	if err != nil {
		t.Fatalf("expected migration version: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "ao.db"), migratedDatabaseSnapshot(t, want-1), 0o600); err != nil {
		t.Fatalf("write stale db: %v", err)
	}

	_, err = OpenPreMigrated(dataDir)
	if err == nil {
		t.Fatal("OpenPreMigrated succeeded on stale database; want error")
	}
}

func openMigratedTestDBIn(t *testing.T, dataDir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func createEmptyDatabase(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.Ping()
}
