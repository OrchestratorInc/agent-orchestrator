package sqlite

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateLiveDatabaseCopy dry-runs the full migration chain against a
// snapshot of the owner's real database BEFORE a rebuilt app launches against
// the original. ao-sync.sh copies ~/.ao/data/ao.db with sqlite3 .backup and
// points AO_SYNC_LIVE_DB at the copy; without the variable the test skips so
// the normal suite stays hermetic. A renumbered or conflicting migration then
// fails the sync in seconds with the exact boot error, instead of wedging the
// daemon after a full rebuild (the 2026-10-25 report_emitted_at outage).
func TestMigrateLiveDatabaseCopy(t *testing.T) {
	live := os.Getenv("AO_SYNC_LIVE_DB")
	if live == "" {
		t.Skip("AO_SYNC_LIVE_DB not set; live-database dry-run runs from ao-sync.sh")
	}
	src, err := os.Open(live)
	if err != nil {
		t.Fatalf("open live database copy %s: %v", live, err)
	}
	defer func() { _ = src.Close() }()
	dir := t.TempDir()
	dst, err := os.OpenFile(filepath.Join(dir, "ao.db"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := migrate(db); err != nil {
		t.Fatalf("live database would fail to migrate at daemon boot: %v", err)
	}
}
