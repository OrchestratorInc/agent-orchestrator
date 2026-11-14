package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	beforeJCodeVersion = 20261114072016988
	jcodeVersion       = 20261114172702974
)

// TestMigrationAllowJCodeHarnessAllowsJCodeAndReverses mirrors the Hermes
// coverage: JCode becomes insertable, unknown harnesses stay rejected, and the
// down migration restores the exact prior constraint once no JCode row remains.
func TestMigrationAllowJCodeHarnessAllowsJCodeAndReverses(t *testing.T) {
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
	upTo(t, db, jcodeVersion)
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
	db := openMigratedDatabaseCopy(t, jcodeVersion)
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

// TestJCodeMigrationAfterEarlierBranchNumber covers a development database
// that applied this branch's migration under its earlier number,
// 20261030135805920: the renumbered migration finds JCode already allowed and
// changes nothing, and main's open usage-kind CHECKs accept JCode usage rows.
func TestJCodeMigrationAfterEarlierBranchNumber(t *testing.T) {
	db := openMigratedDatabaseCopy(t, jcodeVersion)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `DELETE FROM goose_db_version WHERE version_id = ?`, jcodeVersion)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (20261030135805920, 1)`)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate a database that applied the earlier number: %v", err)
	}
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("sessions schema changed on reapply: %v\n%s\nwant\n%s", err, after, before)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('ju-project', '/tmp/ju-project', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES ('ju-1', 'ju-project', 1, 'jcode', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at) VALUES ('ju-1', 'jcode', 'session_x_1_0000000000000000', 'active', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO usage_sources (binding_id, kind, artifact_path, state, updated_at) VALUES ((SELECT id FROM usage_bindings WHERE harness = 'jcode'), 'jcode_ledger', '/tmp/ledger.jsonl', 'active', CURRENT_TIMESTAMP)`)
}
