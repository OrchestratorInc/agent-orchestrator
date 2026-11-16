package sqlite

import (
	"context"
	"testing"
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
	db := openMigratedTestDB(t)
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
	if result, err := runTimestampRepair(ctx, db, false); err != nil || result.Skipped != 0 {
		t.Fatalf("ordinary writes repeated full repair: %+v %v", result, err)
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
