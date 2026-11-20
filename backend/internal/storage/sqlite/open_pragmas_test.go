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
	repairDone := make(chan struct{})
	store, err := openWithTimestampOptions(dir, timestampOpenOptions{fenceBudget: timestampFenceTimeout, repairDone: repairDone})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	<-repairDone

	// Inflate the log from a connection that has no limit, with a reader
	// deliberately preventing its passive automatic checkpoint from finishing.
	burst, err := sql.Open("sqlite", plainDSN(dir))
	if err != nil {
		t.Fatalf("open burst connection: %v", err)
	}
	burst.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = burst.Close() })
	if _, err := burst.Exec(`CREATE TABLE wal_burst (payload BLOB)`); err != nil {
		t.Fatalf("create burst table: %v", err)
	}
	reader, err := sql.Open("sqlite", databaseURI(dir)+readOnlyPragmas)
	if err != nil {
		t.Fatalf("open held reader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	readTx, err := reader.Begin()
	if err != nil {
		t.Fatalf("begin held read: %v", err)
	}
	t.Cleanup(func() { _ = readTx.Rollback() })
	var rows int
	if err := readTx.QueryRow(`SELECT COUNT(*) FROM wal_burst`).Scan(&rows); err != nil {
		t.Fatalf("hold pre-burst snapshot: %v", err)
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
	if err := readTx.Rollback(); err != nil {
		t.Fatalf("release held reader: %v", err)
	}
	// A completed RESTART makes the next writer reset the WAL, but unlike
	// TRUNCATE it leaves the oversized allocation for AO's writer to trim.
	var busy, frames, checkpointed int
	if err := burst.QueryRow(`PRAGMA wal_checkpoint(RESTART)`).Scan(&busy, &frames, &checkpointed); err != nil {
		t.Fatalf("prepare WAL reset: %v", err)
	}
	if busy != 0 || frames < 0 || frames != checkpointed {
		t.Fatalf("WAL is not ready to reset: busy=%d frames=%d checkpointed=%d", busy, frames, checkpointed)
	}
	ready, err := os.Stat(wal)
	if err != nil {
		t.Fatalf("stat WAL before reset: %v", err)
	}
	if ready.Size() <= walSizeLimit {
		t.Fatalf("setup trimmed WAL to %d bytes before AO's writer could enforce its limit", ready.Size())
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
