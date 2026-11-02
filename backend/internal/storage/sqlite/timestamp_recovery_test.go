package sqlite

import (
	"context"
	"database/sql"
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
