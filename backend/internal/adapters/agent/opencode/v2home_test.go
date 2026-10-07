package opencode

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrepareV2DataHomeSnapshotsLiveSQLiteDatabase(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	legacy := filepath.Join(parent, "opencode")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(legacy, "opencode.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)&_pragma=synchronous(FULL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id) VALUES ('one')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id) VALUES ('two')`); err != nil {
		t.Fatal(err)
	}
	// Keep the raw copier between the database and WAL long enough to force a
	// checkpoint after it has copied the database file but before it copies WAL.
	padding := make([]byte, 8<<20)
	if err := os.WriteFile(filepath.Join(legacy, "opencode.db-padding"), padding, 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := PrepareV2DataHome(context.Background())
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		staging, _ := filepath.Glob(filepath.Join(parent, v2DataHomeDirName, ".opencode-migration-*", "opencode.db-padding"))
		if len(staging) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("migration did not reach the post-database padding file")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	migrated, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(parent, v2DataHomeDirName, "opencode", "opencode.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	var count int
	if err := migrated.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("migrated sessions = %d, want a consistent two-session snapshot", count)
	}
}

func TestPrepareV2DataHomeDoesNotCopyOrphanedRollbackJournal(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	legacy := filepath.Join(parent, "opencode")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "opencode.db-journal"), []byte("orphaned journal"), 0o600); err != nil {
		t.Fatal(err)
	}

	home, err := PrepareV2DataHome(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "opencode", "opencode.db-journal")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("migrated store retained rollback journal: %v", err)
	}
}

func TestPrepareV2DataHomeStopsWaitingForMigrationLockOnCancel(t *testing.T) {
	parent := t.TempDir()
	t.Setenv("XDG_DATA_HOME", parent)
	legacy := filepath.Join(parent, "opencode")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(legacy, "opencode.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`ROLLBACK`) }()

	firstDone := make(chan error, 1)
	go func() {
		_, err := PrepareV2DataHome(context.Background())
		firstDone <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		staging, _ := filepath.Glob(filepath.Join(parent, v2DataHomeDirName, ".opencode-migration-*"))
		if len(staging) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first migration did not acquire the migration lock")
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := PrepareV2DataHome(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second migration error = %v, want deadline while waiting for lock", err)
	}
	if _, err := db.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first migration did not finish after releasing SQLite lock")
	}
}

func TestCopyTreeStopsOnCancel(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "auth.json"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyTree(ctx, source, destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("copyTree error = %v, want context canceled", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled copy created destination file: %v", err)
	}
}

func TestSnapshotSQLiteDatabasePreservesDeadline(t *testing.T) {
	source := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(source))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`ROLLBACK`) }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = snapshotSQLiteDatabase(ctx, source, filepath.Join(t.TempDir(), "snapshot.db"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("snapshot error = %v, want deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("snapshot ignored deadline for %s", elapsed)
	}
}
