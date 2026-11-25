package sqlite

import (
	"context"
	"database/sql"
	"math"
	"testing"
)

func timestampTriggerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE repair_target(id INTEGER PRIMARY KEY,at TIMESTAMP,other_at TIMESTAMP,payload TEXT);
CREATE TABLE goose_db_version(id INTEGER PRIMARY KEY,version_id INTEGER,is_applied INTEGER);
INSERT INTO goose_db_version VALUES(1,1,1);
CREATE TABLE application_audit(id INTEGER);
CREATE TRIGGER application_update AFTER UPDATE OF payload ON repair_target BEGIN INSERT INTO application_audit VALUES(NEW.id); END;`); err != nil {
		t.Fatal(err)
	}
	columns, err := timestampColumns(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareTimestampProgress(context.Background(), db, columns); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTimestampCanonicalWritesAllowPendingRepairToComplete(t *testing.T) {
	db := timestampTriggerDB(t)
	ctx := context.Background()
	generation, _, err := beginTimestampProgress(ctx, db, "repair_target", "test")
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if _, err := db.Exec(`INSERT INTO repair_target VALUES(1,'2026-10-21 03:30:00 +0000 UTC',CURRENT_TIMESTAMP,'before');
UPDATE repair_target SET payload='after',at='2026-10-21 04:30:00 +0000 UTC' WHERE id=1;
DELETE FROM repair_target WHERE id=1;`); err != nil {
			t.Fatal(err)
		}
	}
	if err := finishTimestampProgress(ctx, db, "repair_target", "test", generation, false); err != nil {
		t.Fatalf("normal writes kept repair pending: %v", err)
	}
}

func TestTimestampSchemaMigrationCanDropTrackedColumns(t *testing.T) {
	for _, last := range []bool{false, true} {
		t.Run(map[bool]string{false: "one timestamp remains", true: "last timestamp removed"}[last], func(t *testing.T) {
			db := timestampTriggerDB(t)
			ctx := context.Background()
			triggers, err := prepareTimestampSchemaMigration(ctx, db, []int64{1, 2})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`ALTER TABLE repair_target DROP COLUMN at`); err != nil {
				t.Fatalf("tracking trigger blocked migration: %v", err)
			}
			if last {
				if _, err := db.Exec(`ALTER TABLE repair_target DROP COLUMN other_at`); err != nil {
					t.Fatal(err)
				}
			}
			if err := restoreTimestampProgressTriggers(ctx, db, triggers); err != nil {
				t.Fatal(err)
			}
			columns, err := timestampColumns(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			if err := prepareTimestampProgress(ctx, db, columns); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO repair_target(id,payload) VALUES(1,'before'); UPDATE repair_target SET payload='after' WHERE id=1`); err != nil {
				t.Fatalf("stale column reference broke writes: %v", err)
			}
			var events int
			if err := db.QueryRow(`SELECT count(*) FROM application_audit`).Scan(&events); err != nil || events != 1 {
				t.Fatalf("application trigger changed: events=%d err=%v", events, err)
			}
		})
	}
}

func TestTimestampCurrentSchemaKeepsCompletion(t *testing.T) {
	db := timestampTriggerDB(t)
	ctx := context.Background()
	generation, _, err := beginTimestampProgress(ctx, db, "repair_target", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := finishTimestampProgress(ctx, db, "repair_target", "test", generation, false); err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err := db.QueryRow(`PRAGMA schema_version`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	previous, err := prepareTimestampSchemaMigration(ctx, db, []int64{1})
	if err != nil || len(previous) != 0 {
		t.Fatalf("ordinary open suspended triggers: triggers=%d err=%v", len(previous), err)
	}
	if err := db.QueryRow(`PRAGMA schema_version`).Scan(&after); err != nil || before != after {
		t.Fatalf("ordinary open rewrote trigger DDL: schema_version=%d -> %d err=%v", before, after, err)
	}
	if err := restoreTimestampProgressTriggers(ctx, db, previous); err != nil {
		t.Fatal(err)
	}
	_, complete, err := beginTimestampProgress(ctx, db, "repair_target", "test")
	if err != nil || !complete {
		t.Fatalf("ordinary open discarded completion: complete=%v err=%v", complete, err)
	}
	var triggers int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name GLOB 'ao_timestamp_*'`).Scan(&triggers); err != nil || triggers != 3 {
		t.Fatalf("ordinary open changed tracking: triggers=%d err=%v", triggers, err)
	}
}

func TestTimestampMissingOlderMigrationInvalidatesCompletion(t *testing.T) {
	db := timestampTriggerDB(t)
	ctx := context.Background()
	generation, _, err := beginTimestampProgress(ctx, db, "repair_target", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := finishTimestampProgress(ctx, db, "repair_target", "test", generation, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO goose_db_version VALUES(2,3,1)`); err != nil {
		t.Fatal(err)
	}
	// Matching the highest receipt is insufficient with goose.WithAllowMissing.
	triggers, err := prepareTimestampSchemaMigration(ctx, db, []int64{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO repair_target(id,at) VALUES(1,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := restoreTimestampProgressTriggers(ctx, db, triggers); err != nil {
		t.Fatal(err)
	}
	var complete bool
	if err := db.QueryRow(`SELECT completed FROM timestamp_repair_progress WHERE table_name='repair_target'`).Scan(&complete); err != nil || complete {
		t.Fatalf("untracked migration writes could inherit completion: complete=%v err=%v", complete, err)
	}
}

func TestTimestampRejectedBatchInvalidatesBeforePassFinishes(t *testing.T) {
	db := timestampTriggerDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`ALTER TABLE repair_target ADD COLUMN keep_local INTEGER CHECK(id<>1 OR at NOT GLOB '* +0000 UTC');
 WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<257)
 INSERT INTO repair_target(id,at) SELECT i,CASE WHEN i IN (1,257) THEN '2026-10-21 09:00:00 +0530 IST' ELSE '2026-10-21 03:30:00 +0000 UTC' END FROM ids`); err != nil {
		t.Fatal(err)
	}
	columns, err := timestampColumns(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	generation, _, err := beginTimestampProgress(ctx, db, "repair_target", "test")
	if err != nil {
		t.Fatal(err)
	}
	state := newTimestampRepairState()
	next, done, err := repairTimestampBatchMode(ctx, db, columns, 0, false, math.MaxInt64, state)
	if err != nil || done || state.Skipped != 1 {
		t.Fatalf("first batch: next=%d done=%v skipped=%d err=%v", next, done, state.Skipped, err)
	}
	var rejected bool
	if err := db.QueryRow(`SELECT rejected FROM timestamp_repair_progress WHERE table_name='repair_target'`).Scan(&rejected); err != nil || !rejected {
		t.Fatalf("rejection not persisted before batch commit: rejected=%v err=%v", rejected, err)
	}
	// A canonical conflict can disappear while the pass is between batches.
	if _, err := db.Exec(`DELETE FROM repair_target WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	_, done, err = repairTimestampBatchMode(ctx, db, columns, next, false, math.MaxInt64, state)
	if err != nil || !done || state.Repaired != 1 {
		t.Fatalf("second batch: done=%v repaired=%d err=%v", done, state.Repaired, err)
	}
	current, _, err := beginTimestampProgress(ctx, db, "repair_target", "test")
	if err != nil || current != generation+1 {
		t.Fatalf("external delete must invalidate, repair writes must stay paused: generation=%d want=%d err=%v", current, generation+1, err)
	}
	if err := finishTimestampProgress(ctx, db, "repair_target", "test", generation, true); err == nil {
		t.Fatal("inter-batch write allowed stale completion")
	}
}
