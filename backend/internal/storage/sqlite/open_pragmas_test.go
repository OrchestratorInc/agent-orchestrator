package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const walSizeLimit = 32 << 20

// plainDSN opens ao.db without AO's pragmas, so no limit is set on the
// connection and its commits leave the -wal file at whatever size it reached.
func plainDSN(dataDir string) string {
	return databaseURI(dataDir) + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
}

func statRows(t *testing.T, dataDir, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", databaseURI(dataDir)+"?mode=ro")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'sqlite_stat1'`).Scan(&present); err != nil {
		t.Fatalf("look for sqlite_stat1: %v", err)
	}
	if present == 0 {
		return 0
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_stat1 WHERE tbl = ?`, table).Scan(&rows); err != nil {
		t.Fatalf("count sqlite_stat1 rows for %s: %v", table, err)
	}
	return rows
}

// A database that already holds data but was never analyzed (every database
// that predates this change) gets planner statistics the next time it is opened.
func TestOpenCollectsPlannerStatisticsForPopulatedTables(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("project-%d", i)
		if err := store.UpsertProject(context.Background(), domain.ProjectRecord{ID: id, Path: filepath.Join(dir, id)}); err != nil {
			t.Fatalf("seed project: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}
	if got := statRows(t, dir, "projects"); got != 0 {
		t.Fatalf("projects already has %d sqlite_stat1 rows; the fixture must start unanalyzed", got)
	}

	store, err = Open(dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close second store: %v", err)
	}
	if got := statRows(t, dir, "projects"); got == 0 {
		t.Fatal("reopening a populated database wrote no sqlite_stat1 rows for projects")
	}
}

func TestPragmasSetJournalSizeLimit(t *testing.T) {
	db, err := sql.Open("sqlite", databaseURI(t.TempDir())+pragmas)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()
	var limit int64
	if err := db.QueryRow(`PRAGMA journal_size_limit`).Scan(&limit); err != nil {
		t.Fatalf("read journal_size_limit: %v", err)
	}
	if limit != walSizeLimit {
		t.Fatalf("journal_size_limit = %d, want %d", limit, walSizeLimit)
	}
}

// A burst that grows the -wal file past the cap (a large migration, or a reader
// holding back the checkpoint) must not leave it at that size for the life of
// the daemon: the writer Open returns cuts it back at the next WAL reset.
func TestOpenWriterTrimsAnInflatedWALAtTheNextReset(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Inflate the log from a connection that has no limit. It checkpoints the
	// log into the database right after the commit, so the writer is free to
	// reset it on its next transaction.
	burst, err := sql.Open("sqlite", plainDSN(dir))
	if err != nil {
		t.Fatalf("open burst connection: %v", err)
	}
	burst.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = burst.Close() })
	if _, err := burst.Exec(`CREATE TABLE wal_burst (payload BLOB)`); err != nil {
		t.Fatalf("create burst table: %v", err)
	}
	if _, err := burst.Exec(`INSERT INTO wal_burst VALUES (zeroblob(?))`, walSizeLimit+(2<<20)); err != nil {
		t.Fatalf("write burst: %v", err)
	}
	wal := filepath.Join(dir, "ao.db-wal")
	grown, err := os.Stat(wal)
	if err != nil {
		t.Fatalf("stat wal: %v", err)
	}
	if grown.Size() <= walSizeLimit {
		t.Fatalf("setup left the -wal file at %d bytes; it must exceed the %d byte cap", grown.Size(), walSizeLimit)
	}

	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{ID: "after-burst", Path: filepath.Join(dir, "repo")}); err != nil {
		t.Fatalf("write after burst: %v", err)
	}
	trimmed, err := os.Stat(wal)
	if err != nil {
		t.Fatalf("stat wal after write: %v", err)
	}
	if trimmed.Size() > walSizeLimit {
		t.Fatalf("-wal file is %d bytes after the next reset, want at most %d", trimmed.Size(), walSizeLimit)
	}
}
