package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestTimestampFenceInterruptedAndRetried(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<257)
 INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
 SELECT 'fence-'||i,i,'aider','idle','2026-10-21 09:00:00 +0530 IST',0,'2026-10-21 09:00:00 +0530 IST','2026-10-21 09:00:00 +0530 IST' FROM ids`); err != nil {
		t.Fatal(err)
	}
	columns, err := timestampColumns(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	var group []timestampColumn
	for _, c := range columns {
		if c.table == "sessions" {
			group = append(group, c)
		}
	}
	if _, done, err := repairTimestampBatch(context.Background(), db, group, -1<<63); err != nil || done {
		t.Fatalf("first committed batch: done=%v err=%v", done, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := repairTimestamps(ctx, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted fence: %v", err)
	}
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM sessions WHERE CAST(created_at AS TEXT) LIKE '%IST'`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
	if _, err := db.Exec(`CREATE TABLE aaa_repair(at TIMESTAMP); INSERT INTO aaa_repair VALUES('2026-10-21 09:00:00 +0530 IST'); CREATE TRIGGER block_background BEFORE UPDATE ON aaa_repair BEGIN SELECT RAISE(ABORT,'background blocked'); END`); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := db.QueryRow(`SELECT count(*) FROM sessions WHERE CAST(created_at AS TEXT) LIKE '%IST'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("retry left %d legacy rows: %v", remaining, err)
	}
}

func TestTimestampFenceFailureDoesNotPublishReaders(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('fence','/tmp/fence','2026-10-21 03:30:00 +0000 UTC'); INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
 VALUES('fence',1,'aider','idle','2026-10-21 09:00:00 +0530 IST',0,'2026-10-21 09:00:00 +0530 IST','2026-10-21 09:00:00 +0530 IST');
 INSERT INTO reports(id,session_id,project_id,created_at,available_at) VALUES('blocked','fence','fence','2026-10-21 09:00:00 +0530 IST','2026-10-21 09:00:00 +0530 IST');
 CREATE TRIGGER block_fence BEFORE UPDATE ON reports BEGIN SELECT RAISE(ABORT,'fence blocked'); END;`); err != nil {
		t.Fatal(err)
	}
	first, err := Open(dir)
	if err == nil {
		_ = first.Close()
		t.Fatal("failed ordering repair published readers")
	}
	var raw string
	if err := db.QueryRow(`SELECT CAST(created_at AS TEXT) FROM sessions`).Scan(&raw); err != nil || !strings.HasSuffix(raw, "+0000 UTC") {
		t.Fatalf("later table stalled: %q %v", raw, err)
	}
	if _, err := db.Exec(`DROP TRIGGER block_fence`); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
}

func TestTimestampRepairTriggerInventory(t *testing.T) {
	db := openMigratedTestDB(t)
	columns, err := timestampColumns(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, column := range columns {
		seen[column.table] = true
	}
	for table := range timestampFenceTables {
		if !seen[table] {
			t.Errorf("fence table %q has no timestamp columns", table)
		}
	}
	// Every trigger on a repaired table must be reviewed for representation-only
	// updates. CDC is removed transactionally; only sessions changes revision.
	rows, err := db.Query(`SELECT name,sql FROM sqlite_master WHERE type='trigger'
 AND tbl_name IN (SELECT m.name FROM sqlite_master m,pragma_table_info(m.name) p
 WHERE m.type='table' AND upper(p.type) IN ('TIMESTAMP','DATETIME','DATE')) ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	hash := sha256.New()
	for rows.Next() {
		var name, sql string
		if err := rows.Scan(&name, &sql); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(hash, "%s\n%s\n", name, sql)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// review_run_worker_notice_pending only fires on status updates. Timestamp
	// repair preserves notice state (TestTimestampRepairPreservesReviewDoneNotices).
	const reviewed = "48a989371865b3d88341af7ee9507959e09a14add56c9e65da165d11ad765377"
	if got := fmt.Sprintf("%x", hash.Sum(nil)); got != reviewed {
		t.Fatalf("timestamp trigger inventory changed; review preservation logic before updating fingerprint: %s", got)
	}
}

func TestTimestampRepairIndexedBounds(t *testing.T) {
	db := openRepairDB(t)
	if _, err := db.Exec(`CREATE INDEX repair_at ON repair_times(at);
 WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<600)
 INSERT INTO repair_times(id,at) SELECT i,'2026-10-21 09:00:00 +0530 IST' FROM ids`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	columns := []timestampColumn{{table: "repair_times", column: "at"}}
	first, last, done, err := timestampRepairBounds(ctx, db, columns)
	if err != nil || done || first != 1 || last != 600 {
		t.Fatalf("dense bounds=%d,%d done=%v err=%v", first, last, done, err)
	}
	batches := 0
	for !done {
		first, done, err = repairTimestampBatchMode(ctx, db, columns, first, true, last, nil)
		if err != nil {
			t.Fatal(err)
		}
		batches++
	}
	if batches != 3 {
		t.Fatalf("dense repair took %d batches, want 3", batches)
	}
	if _, _, done, err := timestampRepairBounds(ctx, db, columns); err != nil || !done {
		t.Fatalf("clean index done=%v err=%v", done, err)
	}
	if _, err := db.Exec(`UPDATE repair_times SET at='2026-10-21 09:00:00 +0530 IST' WHERE id=300`); err != nil {
		t.Fatal(err)
	}
	if first, last, done, err := timestampRepairBounds(ctx, db, columns); err != nil || done || first != 300 || last != 300 {
		t.Fatalf("sparse bounds=%d,%d done=%v err=%v", first, last, done, err)
	}
}

func TestOpenRetriesCommittedProgressAfterStartupRepairDeadline(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<2000)
 INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
 SELECT 'late-'||i,i,'aider','idle','2026-10-21 09:00:00 +0530 IST',0,'2026-10-21 09:00:00 +0530 IST','2026-10-21 09:00:00 +0530 IST' FROM ids`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	counts, done := &repairScanCounts{}, make(chan struct{})
	counts.expireAfterBatch.Store(true)
	opts := repairScanOptions(t, counts, done)
	// Inject a deadline into the second scan after one committed batch.
	// Committed progress must survive and the background pass resumes.
	s, err := openWithTimestampOptions(dir, opts)
	if !errors.Is(err, context.DeadlineExceeded) {
		if s != nil {
			_ = s.Close()
		}
		t.Fatalf("deadline must withhold readers: %v", err)
	}
	if n := counts.fenceRows.Load(); n != timestampRepairBatch {
		t.Errorf("fence repaired %d rows before its deadline, want %d", n, timestampRepairBatch)
	}
	if strings.Contains(logs.String(), "timestamp startup fence completed") {
		t.Fatalf("misleading deadline log: %s", logs.String())
	}
	// Already committed rows survive; retry the unfinished table on reopen.
	s, err = openWithTimestampOptions(dir, repairScanOptions(t, counts, done))
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var legacy int
	if err := db.QueryRow(`SELECT count(*) FROM sessions WHERE CAST(created_at AS TEXT) LIKE '%IST'`).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("reopen left %d legacy rows: %v", legacy, err)
	}
}
