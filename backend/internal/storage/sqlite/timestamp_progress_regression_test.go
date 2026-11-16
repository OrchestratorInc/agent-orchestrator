package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"
)

func TestTimestampProgressCanonicalWritesWhilePending(t *testing.T) {
	db := openMigratedTestDB(t)
	ctx := context.Background()
	columns, err := timestampColumns(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareTimestampProgress(ctx, db, columns); err != nil {
		t.Fatal(err)
	}
	generation, _, err := beginTimestampProgress(ctx, db, "projects", "pending-test")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO projects(id,path,registered_at) VALUES('utc','/tmp/utc','2026-10-21 03:30:00 +0000 UTC')`,
		`UPDATE projects SET registered_at='2026-10-22 03:30:00 +0000 UTC' WHERE id='utc'`,
		`DELETE FROM projects WHERE id='utc'`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
		var got int64
		if err := db.QueryRow(`SELECT generation FROM timestamp_repair_progress WHERE table_name='projects'`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != generation {
			t.Errorf("canonical write invalidated pending repair: generation %d -> %d after %s", generation, got, statement)
		}
	}
	if err := finishTimestampProgress(ctx, db, "projects", "pending-test", generation, false); err != nil {
		t.Fatalf("canonical writes prevented completion: %v", err)
	}
}

func TestTimestampProgressCanonicalWritesWithRejectedRow(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE rejected_times(id INTEGER PRIMARY KEY, at TIMESTAMP, updated_at TIMESTAMP, note TEXT,
 CHECK(at IS NULL OR CAST(at AS TEXT) NOT GLOB '* +0000 UTC'));
 INSERT INTO rejected_times VALUES(1,'2026-10-21 09:00:00 +0530 IST',CURRENT_TIMESTAMP,'kept')`); err != nil {
		t.Fatal(err)
	}
	if result, err := runTimestampRepair(ctx, db, false); err != nil || result.Skipped != 1 {
		t.Fatalf("rejected fixture: %+v %v", result, err)
	}
	var generation int64
	if err := db.QueryRow(`SELECT generation FROM timestamp_repair_progress WHERE table_name='rejected_times'`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO rejected_times VALUES(2,NULL,'2026-10-21 03:30:00 +0000 UTC','canonical')`,
		`UPDATE rejected_times SET updated_at='2026-10-22 03:30:00 +0000 UTC'`,
		`UPDATE rejected_times SET note='still kept' WHERE id=1`,
		`DELETE FROM rejected_times WHERE id=2`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
		var got int64
		var completed bool
		if err := db.QueryRow(`SELECT generation,completed FROM timestamp_repair_progress WHERE table_name='rejected_times'`).Scan(&got, &completed); err != nil {
			t.Fatal(err)
		}
		if got != generation || !completed {
			t.Errorf("canonical write invalidated rejected table: generation %d -> %d, complete=%v after %s", generation, got, completed, statement)
		}
	}
	counts := &repairScanCounts{}
	opts := repairScanOptions(t, counts, nil)
	reader, err := sql.Open(opts.driver, databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if result, err := runTimestampRepairState(ctx, db, reader, false, newTimestampRepairState()); err != nil || result.Repaired != 0 || result.Deleted != 0 {
		t.Fatalf("retry kept row: %+v %v", result, err)
	}
	if scans := counts.reader.Load() + counts.writer.Load() - counts.rejectedLookups.Load(); scans != 0 {
		t.Fatalf("ordinary writes repeated %d full repair scans", scans)
	}
}

func TestTimestampProgressMigrationDropsReferencedColumns(t *testing.T) {
	db := openMigratedTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`ALTER TABLE projects ADD COLUMN removed_at TIMESTAMP`); err != nil {
		t.Fatal(err)
	}
	columns, err := timestampColumns(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareTimestampProgress(ctx, db, columns); err != nil {
		t.Fatal(err)
	}
	// Exercise startup schema repair with installed triggers, rather than a
	// fresh migration replay where no repair triggers exist yet.
	previous := schemaRepairs
	schemaRepairs = append(schemaRepairs[:len(schemaRepairs):len(schemaRepairs)], schemaRepairs[0])
	t.Cleanup(func() { schemaRepairs = previous })
	repair := &schemaRepairs[len(schemaRepairs)-1]
	repair.table, repair.column = "projects", "removed_at_migration"
	repair.addDDL = `ALTER TABLE projects DROP COLUMN removed_at;
 ALTER TABLE projects ADD COLUMN removed_at_migration TEXT;
 INSERT INTO projects(id,path,registered_at) VALUES('migration','/tmp/migration',CURRENT_TIMESTAMP)`
	repair.postAdd = nil
	if err := migrate(db); err != nil {
		t.Fatalf("migration with installed repair triggers: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('after','/tmp/after',CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("write after migration: %v", err)
	}
	if err := repairTimestamps(ctx, db); err != nil {
		t.Fatalf("repair after migration: %v", err)
	}
}

func TestTimestampProgressRetryQueuedBeforeCompletion(t *testing.T) {
	db := openMigratedTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE retry_times(at TIMESTAMP, allow_utc INTEGER,
 CHECK(allow_utc=1 OR CAST(at AS TEXT) NOT GLOB '* +0000 UTC'));
 INSERT INTO retry_times VALUES('2026-10-21 09:00:00 +0530 IST',0)`); err != nil {
		t.Fatal(err)
	}
	if result, err := runTimestampRepair(ctx, db, false); err != nil || result.Skipped != 1 {
		t.Fatalf("kept fixture: %+v %v", result, err)
	}
	// Model the interval after a batch kept a row but before the first pass
	// records the table's rejection summary and completion.
	if _, err := db.Exec(`UPDATE timestamp_repair_progress SET completed=0,rejected=0 WHERE table_name='retry_times'`); err != nil {
		t.Fatal(err)
	}
	var signature string
	var generation int64
	if err := db.QueryRow(`SELECT signature,generation FROM timestamp_repair_progress WHERE table_name='retry_times'`).Scan(&signature, &generation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE retry_times SET allow_utc=1`); err != nil {
		t.Fatal(err)
	}
	if err := finishTimestampProgress(ctx, db, "retry_times", signature, generation, true); err != nil {
		t.Fatalf("canonical write invalidated the full pass: %v", err)
	}
	if result, err := runTimestampRepair(ctx, db, false); err != nil || result.Repaired != 1 {
		t.Fatalf("write before completion lost the kept-row retry: %+v %v", result, err)
	}
}

func TestTimestampProgressDropsOrphanTriggers(t *testing.T) {
	for _, change := range []string{
		// Model SQLite versions which permit the drop and leave invalid
		// trigger references behind. Newer versions reject the DDL itself.
		`PRAGMA writable_schema=ON; ALTER TABLE last_timestamp DROP COLUMN at; PRAGMA writable_schema=OFF`,
		`ALTER TABLE last_timestamp RENAME TO renamed_table`,
	} {
		t.Run(change, func(t *testing.T) {
			db := openMigratedTestDB(t)
			ctx := context.Background()
			if _, err := db.Exec(`CREATE TABLE last_timestamp(id INTEGER PRIMARY KEY, at TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			if err := repairTimestamps(ctx, db); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(change); err != nil {
				t.Fatal(err)
			}
			if err := repairTimestamps(ctx, db); err != nil {
				t.Fatal(err)
			}
			var orphaned int
			if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name GLOB 'ao_timestamp_*' AND tbl_name IN ('last_timestamp','renamed_table') AND sql LIKE '%table_name=''last_timestamp''%'`).Scan(&orphaned); err != nil {
				t.Fatal(err)
			}
			if orphaned != 0 {
				t.Fatalf("%d stale triggers survived schema change", orphaned)
			}
		})
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

func TestTimestampProgressMigrationKeepsLegacyWritesVisible(t *testing.T) {
	for name, ddl := range map[string]string{
		"write": `INSERT INTO migration_times VALUES(1,'2026-10-21 09:00:00 +0530 IST');
 ALTER TABLE migration_times ADD COLUMN migration_marker TEXT`,
		"rebuild": `DROP TABLE migration_times;
 CREATE TABLE migration_times(id INTEGER PRIMARY KEY, at TIMESTAMP, migration_marker TEXT);
 INSERT INTO migration_times VALUES(1,'2026-10-21 09:00:00 +0530 IST',NULL)`,
		"rebuild keeping old table": `ALTER TABLE migration_times RENAME TO migration_backup;
 CREATE TABLE migration_times(id INTEGER PRIMARY KEY, at TIMESTAMP, migration_marker TEXT);
 INSERT INTO migration_times VALUES(1,'2026-10-21 09:00:00 +0530 IST',NULL)`,
	} {
		t.Run(name, func(t *testing.T) {
			db := openMigratedTestDB(t)
			ctx := context.Background()
			if _, err := db.Exec(`CREATE TABLE migration_times(id INTEGER PRIMARY KEY, at TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			if err := repairTimestamps(ctx, db); err != nil {
				t.Fatal(err)
			}
			previous := schemaRepairs
			schemaRepairs = append(schemaRepairs[:len(schemaRepairs):len(schemaRepairs)], schemaRepairs[0])
			t.Cleanup(func() { schemaRepairs = previous })
			repair := &schemaRepairs[len(schemaRepairs)-1]
			repair.table, repair.column, repair.addDDL = "migration_times", "migration_marker", ddl
			repair.postAdd = nil
			if err := migrate(db); err != nil {
				t.Fatal(err)
			}
			result, err := runTimestampRepair(ctx, db, false)
			if err != nil || result.Repaired != 1 {
				t.Fatalf("migration hid legacy write: %+v %v", result, err)
			}
			var got string
			if err := db.QueryRow(`SELECT CAST(at AS TEXT) FROM migration_times WHERE id=1`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != "2026-10-21 03:30:00 +0000 UTC" {
				t.Fatalf("legacy migration value survived: %s", got)
			}
		})
	}
}

func TestTimestampProgressOlderMigrationSetRemainsReadable(t *testing.T) {
	db := openMigratedTestDB(t)
	if _, err := db.Exec(`INSERT INTO projects(id,path,registered_at)
 VALUES('older-reader','/tmp/older-reader','2026-10-21 09:00:00 +0530 IST')`); err != nil {
		t.Fatal(err)
	}
	if err := repairTimestamps(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	older := fstest.MapFS{}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "20261116210724592_timestamp_repair_rejections.sql" {
			continue
		}
		path := "migrations/" + entry.Name()
		content, err := migrationsFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		older[path] = &fstest.MapFile{Data: content}
	}
	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(older)
	defer goose.SetBaseFS(migrationsFS)
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
