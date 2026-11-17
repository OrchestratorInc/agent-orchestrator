package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

type timestampBatchCounts struct{ batches, rows, measurements atomic.Int64 }
type timestampBatchDriver struct {
	driver.Driver
	counts *timestampBatchCounts
}
type timestampBatchConn struct {
	sqliteConn
	counts *timestampBatchCounts
}

var timestampBatchDriverID atomic.Int64

func (d timestampBatchDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	c, ok := conn.(sqliteConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("test driver lacks SQLite interfaces")
	}
	return timestampBatchConn{c, d.counts}, nil
}
func (c timestampBatchConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if q == "SAVEPOINT timestamp_batch" {
		c.counts.batches.Add(1)
	}
	if q == "SAVEPOINT timestamp_row" {
		c.counts.rows.Add(1)
	}
	return c.sqliteConn.ExecContext(ctx, q, args)
}
func (c timestampBatchConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(q, "total_changes()") {
		c.counts.measurements.Add(1)
	}
	return c.sqliteConn.QueryContext(ctx, q, args)
}
func openTimestampBatchDB(t *testing.T) (*sql.DB, *timestampBatchCounts) {
	t.Helper()
	base, err := sql.Open("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	counts := &timestampBatchCounts{}
	name := fmt.Sprintf("timestamp-batch-%d", timestampBatchDriverID.Add(1))
	sql.Register(name, timestampBatchDriver{base.Driver(), counts})
	_ = base.Close()
	db, err := sql.Open(name, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE repair_times(id INTEGER PRIMARY KEY,at TIMESTAMP);
 CREATE TABLE change_log(seq INTEGER PRIMARY KEY AUTOINCREMENT,payload TEXT);
 INSERT INTO change_log(payload) VALUES('preserved');
 CREATE TRIGGER repair_event AFTER UPDATE ON repair_times BEGIN INSERT INTO change_log(payload) VALUES('repair'); END;`); err != nil {
		t.Fatal(err)
	}
	return db, counts
}

func TestTimestampBatchFastPathUsesOneSavepoint(t *testing.T) {
	db, counts := openTimestampBatchDB(t)
	if _, err := db.Exec(`WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<256)
 INSERT INTO repair_times(id,at) SELECT i,'2026-10-21 09:00:00 +0530 IST' FROM ids`); err != nil {
		t.Fatal(err)
	}
	result, err := runTimestampRepair(context.Background(), db, false)
	if err != nil || result.Repaired != 256 || result.Deleted != 0 || result.Skipped != 0 {
		t.Fatalf("repair=%+v err=%v", result, err)
	}
	if counts.batches.Load() != 1 || counts.rows.Load() != 0 || counts.measurements.Load() != 3 {
		t.Fatalf("batch=%d row=%d measurements=%d; want 1,0,3", counts.batches.Load(), counts.rows.Load(), counts.measurements.Load())
	}
	var repaired, logs int
	if err := db.QueryRow(`SELECT count(*) FROM repair_times WHERE CAST(at AS TEXT)='2026-10-21 03:30:00 +0000 UTC'`).Scan(&repaired); err != nil || repaired != 256 {
		t.Fatalf("repaired=%d err=%v", repaired, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM change_log WHERE payload='preserved'`).Scan(&logs); err != nil || logs != 1 {
		t.Fatalf("existing logs=%d err=%v", logs, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM change_log WHERE payload='repair'`).Scan(&logs); err != nil || logs != 0 {
		t.Fatalf("repair events=%d err=%v", logs, err)
	}
}

func TestTimestampBatchConstraintFallbackPreservesChangesAndCounters(t *testing.T) {
	db, counts := openTimestampBatchDB(t)
	if _, err := db.Exec(`INSERT INTO repair_times VALUES(1,'2026-10-21 09:00:00 +0530 IST'),(2,'2026-10-21 09:00:00 +0530 IST'),(3,'2026-10-21 09:00:00 +0530 IST');
 ALTER TABLE repair_times ADD COLUMN keep_local INTEGER CHECK(id<>2 OR at NOT GLOB '* +0000 UTC');`); err != nil {
		t.Fatal(err)
	}
	result, err := runTimestampRepair(context.Background(), db, false)
	if err != nil || result.Repaired != 2 || result.Deleted != 0 || result.Skipped != 1 {
		t.Fatalf("repair=%+v err=%v", result, err)
	}
	if counts.batches.Load() != 1 || counts.rows.Load() != 3 {
		t.Fatalf("batch=%d row=%d; want 1,3", counts.batches.Load(), counts.rows.Load())
	}
	var normalized, logs int
	if err := db.QueryRow(`SELECT count(*) FROM repair_times WHERE CAST(at AS TEXT)='2026-10-21 03:30:00 +0000 UTC'`).Scan(&normalized); err != nil || normalized != 2 {
		t.Fatalf("normalized=%d err=%v", normalized, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM change_log`).Scan(&logs); err != nil || logs != 1 {
		t.Fatalf("change logs=%d err=%v", logs, err)
	}
}

func TestTimestampBatchTriggerFailureAborts(t *testing.T) {
	db, _ := openTimestampBatchDB(t)
	if _, err := db.Exec(`INSERT INTO repair_times VALUES(1,'2026-10-21 09:00:00 +0530 IST'),(2,'2026-10-21 09:00:00 +0530 IST');
 CREATE TRIGGER reject_second BEFORE UPDATE ON repair_times WHEN OLD.id=2 BEGIN SELECT RAISE(ABORT,'hold row'); END;`); err != nil {
		t.Fatal(err)
	}
	result, err := runTimestampRepair(context.Background(), db, false)
	if err == nil || !strings.Contains(err.Error(), "hold row") || result.Repaired != 0 || result.Skipped != 0 {
		t.Fatalf("trigger failure was swallowed: repair=%+v err=%v", result, err)
	}
	var normalized int
	if err := db.QueryRow(`SELECT count(*) FROM repair_times WHERE CAST(at AS TEXT)='2026-10-21 03:30:00 +0000 UTC'`).Scan(&normalized); err != nil || normalized != 0 {
		t.Fatalf("failed batch committed changes: normalized=%d err=%v", normalized, err)
	}
}

func TestTimestampBatchCollisionCountsCommittedDeletions(t *testing.T) {
	for _, legacyFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(legacyFirst), func(t *testing.T) {
			db := openMigratedTestDB(t)
			if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('s',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			values := []string{"2026-10-21 03:30:00 +0000 UTC", "2026-10-21 09:00:00 +0530 IST"}
			if legacyFirst {
				values[0], values[1] = values[1], values[0]
			}
			for _, at := range values {
				if _, err := db.Exec(`INSERT INTO session_agent_accounts(session_id,provider,account_id,bound_at) VALUES('s','codex','same',?)`, at); err != nil {
					t.Fatal(err)
				}
			}
			result, err := runTimestampRepair(context.Background(), db, true)
			wantRepaired := 1
			if legacyFirst {
				wantRepaired = 0
			}
			if err != nil || result.Deleted != 1 || result.Repaired != wantRepaired || result.Skipped != 0 {
				t.Fatalf("repair=%+v want repaired=%d deleted=1: %v", result, wantRepaired, err)
			}
			result, err = runTimestampRepair(context.Background(), db, true)
			if err != nil || result.Deleted != 0 || result.Repaired != 0 {
				t.Fatalf("second pass=%+v err=%v", result, err)
			}
		})
	}
}

func TestTimestampBatchRepairsDriverOffsetLayouts(t *testing.T) {
	db, _ := openTimestampBatchDB(t)
	cases := []struct{ stored, want string }{
		{"2026-10-21 09:00:00.123456789+05:30", "2026-10-21 03:30:00.123456789 +0000 UTC"},
		{"2026-10-20 23:30:00-04:00", "2026-10-21 03:30:00 +0000 UTC"},
		{"2026-10-21 03:30:00Z", "2026-10-21 03:30:00 +0000 UTC"},
		{"2026-10-21 03:30", "2026-10-21 03:30:00 +0000 UTC"},
		{"2026-10-21 03:30Z", "2026-10-21 03:30:00 +0000 UTC"},
		{"2026-10-21Z", "2026-10-21 00:00:00 +0000 UTC"},
		{"2026-10-21T03:30", "2026-10-21 03:30:00 +0000 UTC"},
		{"2026-10-21 03:30:00", "2026-10-21 03:30:00"},
		{"2026-10-21", "2026-10-21"},
	}
	for i, c := range cases {
		if _, err := db.Exec(`INSERT INTO repair_times VALUES(?,?)`, i, c.stored); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runTimestampRepair(context.Background(), db, false); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		var got string
		if err := db.QueryRow(`SELECT CAST(at AS TEXT) FROM repair_times WHERE id=?`, i).Scan(&got); err != nil || got != c.want {
			t.Errorf("%q became %q, want %q: %v", c.stored, got, c.want, err)
		}
	}
}
