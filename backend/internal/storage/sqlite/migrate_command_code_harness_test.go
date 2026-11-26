package sqlite

import (
	"strings"
	"testing"
)

// TestMigration0194AllowsCommandCodeAndReversesTheCurrentSchema mirrors the
// fx, DeepSeek, and Codewhale coverage: the new harness must be insertable on
// the schema 0194 upgrades, an unknown harness must still be rejected, and the
// down migration must restore the exact prior constraint.
func TestMigration0194AllowsCommandCodeAndReversesTheCurrentSchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 193)
	upTo(t, db, 193)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('cc-project', '/tmp/cc-project', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'cc-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	mustExec(t, db, insert, "existing-omp", 1, "omp")
	upTo(t, db, 194)
	if _, err := db.Exec(insert, "cc-session", 2, "command-code"); err != nil {
		t.Fatalf("insert command-code session after migration: %v", err)
	}
	var version int
	if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != 194 {
		t.Fatalf("migration version = %d, err = %v; want 194", version, err)
	}
	if _, err := db.Exec(insert, "unknown", 3, "unknown-agent"); err == nil {
		t.Fatal("unknown harness bypassed the CHECK constraint")
	}
	// Clear the new harness value before downgrading to the older contract.
	mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'command-code'`)
	downTo(t, db, 193)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("down migration did not restore original schema: %v", err)
	}
	if _, err := db.Exec(insert, "cc-after-down", 4, "command-code"); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("command-code insertion after downgrade = %v; want CHECK failure", err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
	}
}

// TestReconcileCommandCodeAfterBurnedMigration covers a database that never ran
// an earlier harness migration: goose skips the burned number, the current-schema
// rewrite in 0194 matches nothing, and the repair is the only thing that adds
// Command Code. It must also leave every earlier harness intact, which is the
// failure mode when a migration widens a schema variant the repair also relies on.
func TestReconcileCommandCodeAfterBurnedCommandCodeMigration(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 193)
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (194, 1)`); err != nil {
		t.Fatalf("seed burned Command Code migration: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate burned profile: %v", err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"'fx'", "'gemini'", "'unreal-agent'", "'mimo-code'", "'deepseek-harness'", "'openhands'", "'codewhale'", "'command-code'"} {
		if !strings.Contains(schema, harness) {
			t.Fatalf("repaired sessions constraint lost %s: %s", harness, schema)
		}
	}
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatalf("repeat repair: %v", err)
	}
}
