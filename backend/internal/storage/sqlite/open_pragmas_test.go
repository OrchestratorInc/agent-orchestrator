package sqlite

import (
	"context"
	"database/sql"
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
