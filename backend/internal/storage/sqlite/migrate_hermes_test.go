package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigration0203AllowsHermesAndReversesBothHistoricalSchemas mirrors the
// fx and DeepSeek coverage: the new harness must be insertable on a current and a
// legacy-qm schema, an unknown harness must still be rejected, and the down
// migration must restore the exact prior constraint.
func TestMigration0203AllowsHermesAndReversesBothHistoricalSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 194)
			if legacyQM {
				// The retained legacy 'qm' fixture harness sits before
				// 'codewhale' in every variant 0203 rewrites, so anchor
				// there to produce a schema the migration still recognizes.
				mustExec(t, db, `PRAGMA writable_schema = ON`)
				mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''openhands'', ''codewhale''', '''openhands'', ''qm'', ''codewhale''') WHERE type = 'table' AND name = 'sessions'`)
				mustExec(t, db, `PRAGMA writable_schema = RESET`)
			}
			upTo(t, db, 194)
			var before string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('cc-project', '/tmp/cc-project', CURRENT_TIMESTAMP)`)
			insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'cc-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
			mustExec(t, db, insert, "existing-omp", 1, "omp")
			if legacyQM {
				mustExec(t, db, insert, "existing-qm", 2, "qm")
			}
			upTo(t, db, 203)
			if _, err := db.Exec(insert, "cc-session", 3, "hermes"); err != nil {
				t.Fatalf("insert hermes session after migration: %v", err)
			}
			var version int
			if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != 203 {
				t.Fatalf("migration version = %d, err = %v; want 203", version, err)
			}
			if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			gooseMu.Lock()
			rollbackErr := goose.DownTo(db, "migrations", 196)
			gooseMu.Unlock()
			if rollbackErr == nil {
				t.Fatal("rollback accepted existing hermes session")
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'hermes'`)
			downTo(t, db, 194)
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("down migration did not restore original schema: %v", err)
			}
			if _, err := db.Exec(insert, "cc-after-down", 5, "hermes"); err == nil || !strings.Contains(err.Error(), "CHECK") {
				t.Fatalf("hermes insertion after downgrade = %v; want CHECK failure", err)
			}
			var integrity string
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
			}
		})
	}
}

func TestMigration0203RejectsUnrecognizedConstraint(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 194)
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
	err := goose.UpTo(db, "migrations", 203)
	gooseMu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("unrecognized constraint silently migrated: %v", err)
	}
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 203 AND is_applied = 1`).Scan(&applied); err != nil || applied != 0 {
		t.Fatalf("failed migration marked applied: count = %d, err = %v", applied, err)
	}
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake'', ''other''))', '''fake''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	upTo(t, db, 203)
}

func TestMigration0203RefusesUnrecognizedDownConstraint(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 194)
	upTo(t, db, 203)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''hermes'', ''fake''))', '''fake'', ''hermes''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	gooseMu.Lock()
	err := goose.DownTo(db, "migrations", 196)
	gooseMu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("unrecognized constraint was downgraded: %v", err)
	}
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("failed downgrade changed schema: %v", err)
	}
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake'', ''hermes''))', '''hermes'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	downTo(t, db, 196)
}

func TestMigration0203AddsHermesBeforeLegacySchemaRepair(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 194)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''command-code'', ', '') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	upTo(t, db, 203)
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "'hermes'") || strings.Contains(schema, "'command-code'") {
		t.Fatalf("external migration did not add Hermes to legacy schema: %s", schema)
	}
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatalf("AO legacy repair with Hermes present: %v", err)
	}
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "'hermes'") || !strings.Contains(schema, "'command-code'") {
		t.Fatalf("legacy repair lost a harness: %s", schema)
	}
}
