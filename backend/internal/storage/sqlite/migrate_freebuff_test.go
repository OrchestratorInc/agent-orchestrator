package sqlite

import (
	"strings"
	"testing"
)

const freebuffMigrationVersion = 20261020181743263

// TestMigrationAllowsFreebuffAndReversesSchema checks that Freebuff becomes
// insertable, unknown harnesses stay rejected, and the down migration restores
// the exact prior constraint.
func TestMigrationAllowsFreebuffAndReversesSchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261019220421514)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('fb-project', '/tmp/fb-project', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'fb-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	mustExec(t, db, insert, "existing-memcode", 1, "memcode")
	if _, err := db.Exec(insert, "fb-before", 2, "freebuff"); err == nil {
		t.Fatal("freebuff accepted before its migration")
	}
	upTo(t, db, freebuffMigrationVersion)
	if _, err := db.Exec(insert, "fb-session", 3, "freebuff"); err != nil {
		t.Fatalf("insert freebuff session after migration: %v", err)
	}
	if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
		t.Fatal("unknown harness bypassed the CHECK constraint")
	}
	mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'freebuff'`)
	downTo(t, db, 20261019220421514)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("down migration did not restore original schema: %v", err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
	}
}

func TestFreebuffSchemaRepairAddsMissingHarness(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261019220421514)
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatalf("repair: %v", err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"'memcode'", "'hermes'", "'freebuff'", "'fake'"} {
		if strings.Count(schema, harness) != 1 {
			t.Fatalf("repair lost or duplicated %s: %s", harness, schema)
		}
	}
}
