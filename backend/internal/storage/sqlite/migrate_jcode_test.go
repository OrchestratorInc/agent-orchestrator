package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const beforeJCodeVersion = 20261028124744441

// TestMigration20261030135805920AllowsJCodeAndReverses mirrors the Hermes
// coverage: JCode becomes insertable, unknown harnesses stay rejected, and the
// down migration restores the exact prior constraint once no JCode row remains.
func TestMigration20261030135805920AllowsJCodeAndReverses(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeJCodeVersion)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('jc-project', '/tmp/jc-project', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'jc-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	if _, err := db.Exec(insert, "early", 1, "jcode"); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("jcode insert before migration = %v; want CHECK failure", err)
	}
	upTo(t, db, 20261030135805920)
	mustExec(t, db, insert, "jc-session", 2, "jcode")
	mustExec(t, db, insert, "hermes-session", 3, "hermes")
	if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
		t.Fatal("unknown harness bypassed the CHECK constraint")
	}
	gooseMu.Lock()
	rollbackErr := goose.DownTo(db, "migrations", beforeJCodeVersion)
	gooseMu.Unlock()
	if rollbackErr == nil {
		t.Fatal("rollback accepted an existing jcode session")
	}
	mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'jcode'`)
	downTo(t, db, beforeJCodeVersion)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("down migration did not restore original schema: %v", err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
	}
}

func TestJCodeSchemaRepairAddsMissingHarness(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261030135805920)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''jcode'', ', '') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&schema); err != nil || !strings.Contains(schema, "'jcode', 'fake'))") {
		t.Fatalf("repaired schema lacks jcode: %v", err)
	}
}

// TestJCodeMigratesOverOpenUsageKinds covers an install that already ran
// main's open usage-kind migration before this branch's earlier-numbered
// JCode harness migration existed: goose applies the gap out of order, and
// jcode usage bindings and ledger sources need no further constraint change.
func TestJCodeMigratesOverOpenUsageKinds(t *testing.T) {
	const openUsageKindsVersion = 20261103030401452
	db := openMigratedDatabaseCopy(t, openUsageKindsVersion)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''jcode'', ', '') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	mustExec(t, db, `DELETE FROM goose_db_version WHERE version_id = 20261030135805920`)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate over open usage kinds: %v", err)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('ju-project', '/tmp/ju-project', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES ('ju-1', 'ju-project', 1, 'jcode', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at) VALUES ('ju-1', 'jcode', 'session_x_1_0000000000000000', 'active', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO usage_sources (binding_id, kind, artifact_path, state, updated_at) VALUES ((SELECT id FROM usage_bindings WHERE harness = 'jcode'), 'jcode_ledger', '/tmp/ledger.jsonl', 'active', CURRENT_TIMESTAMP)`)
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 20261030135805920 AND is_applied = 1`).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("jcode harness migration applied rows = %d, %v", applied, err)
	}
}
