package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func TestRepairNormalizesReadableLocalComparisons(t *testing.T) {
	db := openMigratedTestDB(t)
	const local = "2026-10-21 18:15:40.123456789 +0200 CEST"
	_, err := db.Exec(`INSERT INTO sessions (id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at,latest_user_prompt,latest_user_prompt_at) VALUES ('cest',1,'aider','idle',?,0,?,?, 'earlier',?)`, local, local, local, local)
	if err != nil {
		t.Fatal(err)
	}
	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatal(err)
	}
	later := time.Date(2026, 10, 21, 16, 30, 0, 0, time.UTC)
	changed, err := gen.New(db).RecordSessionHumanMessage(context.Background(), gen.RecordSessionHumanMessageParams{ID: "cest", LatestUserPrompt: "later", LatestUserPromptAt: sql.NullTime{Time: later, Valid: true}})
	if err != nil || changed != 1 {
		t.Fatalf("later UTC prompt changed %d rows, want 1: %v", changed, err)
	}
	var got time.Time
	if err := db.QueryRow(`SELECT created_at FROM sessions WHERE id='cest'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestOpenServesWhenTimestampRepairCannotWrite(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	_, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES ('bad','/tmp/bad','2026-10-21 18:15:40 +0200 +0200'); CREATE TABLE aaa_repair(at TIMESTAMP); INSERT INTO aaa_repair VALUES ('2026-10-21 18:15:40 +0200 +0200'); CREATE TRIGGER block_repair BEFORE UPDATE ON aaa_repair BEGIN SELECT RAISE(ABORT,'repair blocked'); END; PRAGMA user_version=0;`)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("repair blocked storage startup: %v", err)
	}
	defer s.Close()
	projects, err := s.ListProjects(context.Background())
	if err != nil || len(projects) != 1 {
		t.Fatalf("list during repair: %d projects, %v", len(projects), err)
	}
	// Read-only readers need the same fallback, without altering stored bytes.
	ro, err := OpenReadOnly(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err = ro.ListProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = db.QueryRow(`SELECT CAST(registered_at AS TEXT) FROM projects`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "2026-10-21 18:15:40 +0200 +0200" {
		t.Fatalf("blocked repair changed bytes: %q", raw)
	}
}

func TestRepairHundredThousandDistinctTimes(t *testing.T) {
	db := openRepairDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO repair_times(id,at) VALUES (?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i := 0; i < 100001; i++ {
		if _, err := stmt.Exec(i+1, fmt.Sprintf("2026-10-21 18:15:40.%09d +0200 +0200", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatal(err)
	}
	t.Logf("100001 distinct timestamps repaired in %s", time.Since(start))
	var bad int
	if err := db.QueryRow(`SELECT count(*) FROM repair_times WHERE CAST(at AS TEXT) LIKE '%+0200 +0200%'`).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("%d bad values remain: %v", bad, err)
	}
}

// Clean rows must consume the detection budget too. LIMIT after the timestamp
// predicate can otherwise hold the sole writer for a full clean-table scan.
func TestTimestampDetectionPagesLargeCleanTables(t *testing.T) {
	db := openRepairDB(t)
	if _, err := db.Exec(`WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<100001)
 INSERT INTO repair_times(id,at) SELECT i,'2026-10-21 16:15:40 +0000 UTC' FROM ids`); err != nil {
		t.Fatal(err)
	}
	column := timestampColumn{table: "repair_times", column: "at"}
	next, done, err := repairTimestampBatch(context.Background(), db, column, -1<<63)
	if err != nil {
		t.Fatal(err)
	}
	if done || next != 257 {
		t.Fatalf("first clean batch returned next=%d done=%v, want next=257 done=false", next, done)
	}
	// A sparse bad row later in the table must still be reached after clean pages.
	if _, err := db.Exec(`UPDATE repair_times SET at='2026-10-21 18:15:40 +0200 +0200' WHERE id=100000`); err != nil {
		t.Fatal(err)
	}
	for !done {
		previous := next
		next, done, err = repairTimestampBatch(context.Background(), db, column, next)
		if err != nil {
			t.Fatal(err)
		}
		if !done && next-previous != 256 {
			t.Fatalf("batch examined more than its row budget: %d -> %d", previous, next)
		}
	}
	var at time.Time
	if err := db.QueryRow(`SELECT at FROM repair_times WHERE id=100000`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 21, 16, 15, 40, 0, time.UTC); !at.Equal(want) {
		t.Fatalf("sparse repair: %v, want %v", at, want)
	}
}

func TestTimestampRepairCancellationCanRetry(t *testing.T) {
	db := openRepairDB(t)
	if _, err := db.Exec(`INSERT INTO repair_times(id,at) VALUES(1,'2026-10-21 18:15:40 +0200 +0200')`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := repairTimestamps(ctx, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled repair: %v", err)
	}
	if err := canScanTime(db, `SELECT at FROM repair_times WHERE id=1`); err == nil {
		t.Fatal("cancelled repair changed the row")
	}
	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatal(err)
	}
	if err := canScanTime(db, `SELECT at FROM repair_times WHERE id=1`); err != nil {
		t.Fatal(err)
	}
}

func TestTimestampRepairCloseCancelsWorker(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started, finished := make(chan struct{}), make(chan struct{})
	s.StartTimestampRepair(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	})
	<-started
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Close returned before the repair worker finished")
	}
}
