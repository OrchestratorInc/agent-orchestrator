package sqlite

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestTelemetryIncrementalVacuumUpgrade(t *testing.T) {
	db, err := sql.Open("sqlite", databaseURI(t.TempDir())+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	// Apply the pre-PR schema in one pass; no intermediate snapshots are needed.
	func() {
		gooseMu.Lock()
		defer gooseMu.Unlock()
		goose.SetBaseFS(migrationsFS)
		goose.SetLogger(goose.NopLogger())
		if err := goose.SetDialect("sqlite3"); err != nil {
			t.Fatal(err)
		}
		if err := goose.UpTo(db, "migrations", 20261024194546592); err != nil {
			t.Fatal(err)
		}
	}()
	ctx := context.Background()
	seed := func() {
		t.Helper()
		_, err := db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1000)
			INSERT INTO telemetry_event (id, occurred_at, name, source, level, request_id, payload_json)
			SELECT 'vacuum-' || i, '2020-01-01T00:00:00Z', 'test', 'daemon', 'info', '', hex(zeroblob(4096)) FROM n`)
		if err != nil {
			t.Fatal(err)
		}
	}
	pages := func() int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(`PRAGMA page_count`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	seed()
	before := pages()
	if _, err := db.Exec(`DELETE FROM telemetry_event`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var mode int
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil || mode != 2 {
		t.Fatalf("auto_vacuum = %d, error %v, want INCREMENTAL (2)", mode, err)
	}
	if after := pages(); after >= before {
		t.Fatalf("upgrade did not reclaim existing free pages: before=%d after=%d", before, after)
	}
	seed()
	before = pages()
	var filename string
	if err := db.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&filename); err != nil {
		t.Fatal(err)
	}
	fileSize := func() int64 {
		t.Helper()
		if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filename)
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
	bytesBefore := fileSize()
	if _, err := db.Exec(`DELETE FROM telemetry_event`); err != nil {
		t.Fatal(err)
	}
	s := store.NewStore(db, db)
	free, err := s.FreelistCount(ctx)
	if err != nil || free < 256 {
		t.Fatalf("free pages=%d, error=%v, want >=256", free, err)
	}
	if err := s.IncrementalVacuum(ctx, 256); err != nil {
		t.Fatal(err)
	}
	after := pages()
	if reclaimed := before - after; reclaimed <= 0 || reclaimed > 256 {
		t.Fatalf("reclaimed %d pages, want 1..256", reclaimed)
	}
	bytesAfter := fileSize()
	if bytesAfter >= bytesBefore {
		t.Fatalf("database did not shrink: before=%d after=%d bytes", bytesBefore, bytesAfter)
	}
	t.Logf("incremental vacuum reclaimed %d pages; file shrank from %d to %d bytes", before-after, bytesBefore, bytesAfter)
}
