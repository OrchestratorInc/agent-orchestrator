package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	coderAgentsMigrationBase    int64 = 20261028124744441
	coderAgentsMigrationVersion int64 = 20261031044911564
)

// TestMigration20261031044911564AllowsCoderAgentsAndReverses mirrors the Hermes
// coverage: the new harness becomes insertable, unknown harnesses stay rejected,
// and the down migration restores the exact prior constraint.
func TestMigration20261031044911564AllowsCoderAgentsAndReverses(t *testing.T) {
	db := openMigratedDatabaseCopy(t, coderAgentsMigrationBase)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('ca-project', '/tmp/ca-project', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'ca-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	mustExec(t, db, insert, "existing-hermes", 1, "hermes")
	if _, err := db.Exec(insert, "too-early", 2, "coder-agents"); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("coder-agents insertion before migration = %v; want CHECK failure", err)
	}
	upTo(t, db, coderAgentsMigrationVersion)
	if _, err := db.Exec(insert, "ca-session", 3, "coder-agents"); err != nil {
		t.Fatalf("insert coder-agents session after migration: %v", err)
	}
	if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
		t.Fatal("unknown harness bypassed the CHECK constraint")
	}
	gooseMu.Lock()
	rollbackErr := goose.DownTo(db, "migrations", coderAgentsMigrationBase)
	gooseMu.Unlock()
	if rollbackErr == nil {
		t.Fatal("rollback accepted an existing coder-agents session")
	}
	mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'coder-agents'`)
	downTo(t, db, coderAgentsMigrationBase)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("down migration did not restore original schema: %v", err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
	}
}

func TestMigration20261031044911564RejectsUnrecognizedConstraint(t *testing.T) {
	db := openMigratedDatabaseCopy(t, coderAgentsMigrationBase)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''fake'', ''other''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	gooseMu.Lock()
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		gooseMu.Unlock()
		t.Fatal(err)
	}
	err := goose.UpTo(db, "migrations", coderAgentsMigrationVersion)
	gooseMu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("unrecognized constraint silently migrated: %v", err)
	}
}

func TestCoderAgentsLegacyRepairAddsHarnessOnce(t *testing.T) {
	db := openMigratedDatabaseCopy(t, coderAgentsMigrationBase)
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatalf("repair without coder-agents: %v", err)
	}
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatalf("repeated repair: %v", err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"'coder-agents'", "'hermes'", "'memcode'"} {
		if strings.Count(schema, harness) != 1 {
			t.Fatalf("repair lost or duplicated %s: %s", harness, schema)
		}
	}
}
