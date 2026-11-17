package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func timestampPendingMigration(t *testing.T, ddl string) {
	t.Helper()
	gooseMu.Lock()
	goose.AddNamedMigrationContext("20261117000000000_timestamp_probe.go", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, ddl)
		return err
	}, nil)
	gooseMu.Unlock()
	t.Cleanup(func() {
		gooseMu.Lock()
		defer gooseMu.Unlock()
		goose.ResetGlobalMigrations()
	})
}

func TestTimestampProgressUnrelatedPendingMigrationKeepsCompletion(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	ctx := context.Background()
	// Receipt history can span several repair batches as migrations accumulate.
	if _, err := db.Exec(`WITH RECURSIVE receipts(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM receipts WHERE i<257)
INSERT INTO goose_db_version(version_id,is_applied) SELECT (SELECT max(version_id) FROM goose_db_version WHERE is_applied=1),1 FROM receipts`); err != nil {
		t.Fatal(err)
	}
	if err := repairTimestamps(ctx, db); err != nil {
		t.Fatal(err)
	}
	timestampPendingMigration(t, `CREATE TABLE IF NOT EXISTS probe(id INTEGER)`)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := db.QueryRow(`SELECT count(*) FROM timestamp_repair_progress WHERE completed=0 AND table_name<>'goose_db_version'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("unrelated migration discarded completion for %d untouched tables", pending)
	}
	var complete bool
	if err := db.QueryRow(`SELECT completed FROM timestamp_repair_progress WHERE table_name='goose_db_version'`).Scan(&complete); err != nil || complete {
		t.Fatalf("migration receipt write inherited completion: complete=%v err=%v", complete, err)
	}
	counts := &repairScanCounts{excludeTable: "goose_db_version"}
	opts := repairScanOptions(t, counts, nil)
	reader, err := sql.Open(opts.driver, databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := runTimestampRepairState(ctx, db, reader, false, newTimestampRepairState()); err != nil {
		t.Fatal(err)
	}
	if scans := counts.reader.Load() + counts.writer.Load(); scans != 0 {
		t.Fatalf("untouched tables required %d repair SELECTs", scans)
	}
}

func TestTimestampProgressUnrelatedColumnDoesNotRescan(t *testing.T) {
	dir := t.TempDir()
	first, _ := openTimestampProgressStore(t, dir)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`ALTER TABLE projects ADD COLUMN unrelated TEXT`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, counts := openTimestampProgressStore(t, dir)
	if counts.reader.Load() != 0 || counts.writer.Load() != 0 {
		t.Fatalf("unrelated column forced repair scans: reader=%d writer=%d", counts.reader.Load(), counts.writer.Load())
	}
}

func TestTimestampProgressOldSignatureUpgradesOnce(t *testing.T) {
	dir := t.TempDir()
	first, _ := openTimestampProgressStore(t, dir)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`UPDATE timestamp_repair_progress SET signature='v1'||char(0)||(SELECT group_concat(name,char(0)) FROM (SELECT name FROM pragma_table_info('projects') WHERE upper(type) IN ('TIMESTAMP','DATETIME','DATE') ORDER BY cid))||char(0)||(SELECT sql FROM sqlite_master WHERE type='table' AND name='projects') WHERE table_name='projects'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	second, counts := openTimestampProgressStore(t, dir)
	if scans := counts.reader.Load() + counts.writer.Load(); scans != 1 {
		t.Fatalf("old signature upgrade scanned %d tables; want one", scans)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	_, clean := openTimestampProgressStore(t, dir)
	if scans := clean.reader.Load() + clean.writer.Load(); scans != 0 {
		t.Fatalf("upgraded signature repeated %d repair scans", scans)
	}
}

func TestTimestampProgressMigrationDropsReferencedColumns(t *testing.T) {
	for _, last := range []bool{false, true} {
		t.Run(map[bool]string{false: "one remains", true: "last removed"}[last], func(t *testing.T) {
			db := openMigratedTestDB(t)
			ctx := context.Background()
			if _, err := db.Exec(`CREATE TABLE migration_times(id INTEGER PRIMARY KEY,at TIMESTAMP,other_at TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			if err := repairTimestamps(ctx, db); err != nil {
				t.Fatal(err)
			}
			ddl := `ALTER TABLE migration_times DROP COLUMN at`
			if last {
				ddl += `; ALTER TABLE migration_times DROP COLUMN other_at`
			}
			timestampPendingMigration(t, ddl)
			if err := migrate(db); err != nil {
				t.Fatalf("pending migration with installed triggers: %v", err)
			}
			if err := repairTimestamps(ctx, db); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO migration_times(id) VALUES(1)`); err != nil {
				t.Fatalf("stale reference broke writes: %v", err)
			}
			// An unchanged table must retain its original trigger definitions.
			var generation int64
			if err := db.QueryRow(`SELECT generation FROM timestamp_repair_progress WHERE table_name='projects'`).Scan(&generation); err != nil || generation != 1 {
				t.Fatalf("unrelated table was retracked: generation=%d err=%v", generation, err)
			}
		})
	}
}

func TestTimestampProgressDropsOrphanTriggers(t *testing.T) {
	db := openMigratedTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE last_timestamp(id INTEGER PRIMARY KEY,at TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := repairTimestamps(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE last_timestamp RENAME TO renamed_table`); err != nil {
		t.Fatal(err)
	}
	if err := repairTimestamps(ctx, db); err != nil {
		t.Fatal(err)
	}
	var orphaned int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name GLOB 'ao_timestamp_*' AND tbl_name='renamed_table' AND sql LIKE '%table_name=''last_timestamp''%'`).Scan(&orphaned); err != nil {
		t.Fatal(err)
	}
	if orphaned != 0 {
		t.Fatalf("%d stale triggers survived rename", orphaned)
	}
}

func TestTimestampProgressMigrationKeepsLegacyWritesVisible(t *testing.T) {
	for name, ddl := range map[string]string{
		"write": `INSERT INTO migration_times VALUES(1,'2026-10-21 09:00:00 +0530 IST')`,
		"rebuild": `DROP TABLE migration_times;
 CREATE TABLE migration_times(id INTEGER PRIMARY KEY,at TIMESTAMP);
 INSERT INTO migration_times VALUES(1,'2026-10-21 09:00:00 +0530 IST')`,
		"rename and rebuild": `ALTER TABLE migration_times RENAME TO migration_backup;
 CREATE TABLE migration_times(id INTEGER PRIMARY KEY,at TIMESTAMP);
 INSERT INTO migration_times VALUES(1,'2026-10-21 09:00:00 +0530 IST')`,
	} {
		t.Run(name, func(t *testing.T) {
			db := openMigratedTestDB(t)
			ctx := context.Background()
			if _, err := db.Exec(`CREATE TABLE migration_times(id INTEGER PRIMARY KEY,at TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			if err := repairTimestamps(ctx, db); err != nil {
				t.Fatal(err)
			}
			timestampPendingMigration(t, ddl)
			if err := migrate(db); err != nil {
				t.Fatal(err)
			}
			result, err := runTimestampRepair(ctx, db, false)
			if err != nil || result.Repaired != 1 {
				t.Fatalf("migration hid legacy write: %+v %v", result, err)
			}
			var at time.Time
			if err := db.QueryRow(`SELECT at FROM migration_times WHERE id=1`).Scan(&at); err != nil || !at.Equal(time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)) {
				t.Fatalf("legacy migration instant: %v err=%v", at, err)
			}
			var generation int64
			if err := db.QueryRow(`SELECT generation FROM timestamp_repair_progress WHERE table_name='projects'`).Scan(&generation); err != nil || generation != 1 {
				t.Fatalf("migration retracked untouched table: generation=%d err=%v", generation, err)
			}
		})
	}
}

func TestTimestampProgressOlderMigrationSetRemainsReadable(t *testing.T) {
	db := openMigratedTestDB(t)
	if _, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('older-reader','/tmp/older-reader','2026-10-21 09:00:00 +0530 IST')`); err != nil {
		t.Fatal(err)
	}
	if err := repairTimestamps(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	// This fix adds no migrations, so the previous release embeds the same set.
	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(migrationsFS)
	if err := goose.Up(db, "migrations", goose.WithAllowMissing()); err != nil {
		t.Fatalf("older migration set rejected repaired database: %v", err)
	}
	var at time.Time
	if err := db.QueryRow(`SELECT registered_at FROM projects WHERE id='older-reader'`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if !at.Equal(time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)) {
		t.Fatalf("older timestamp read changed instant: %v", at)
	}
	if _, err := db.Exec(`UPDATE projects SET registered_at='2026-10-22 03:30:00 +0000 UTC' WHERE id='older-reader'`); err != nil {
		t.Fatalf("write with installed repair triggers failed: %v", err)
	}
}
