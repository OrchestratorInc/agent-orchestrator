package sqlite

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	openInterpreterMigrationVersion int64 = 20261116125847855
	// beforeOpenInterpreterMigration resolves to the schema just before it.
	beforeOpenInterpreterMigration = openInterpreterMigrationVersion - 1
)

// TestMigration20261116125847855AllowsOpenInterpreterAndReversesBothHistoricalSchemas
// mirrors the Hermes coverage: the new harness must be insertable on a current
// and a legacy-qm schema, an unknown harness must still be rejected, rollback
// must refuse existing rows, and the down migration must restore the exact
// prior constraint.
func TestMigration20261116125847855AllowsOpenInterpreterAndReversesBothHistoricalSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, beforeOpenInterpreterMigration)
			if legacyQM {
				mustExec(t, db, `PRAGMA writable_schema = ON`)
				mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''openhands'', ''codewhale''', '''openhands'', ''qm'', ''codewhale''') WHERE type = 'table' AND name = 'sessions'`)
				mustExec(t, db, `PRAGMA writable_schema = RESET`)
			}
			var before string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('oi-project', '/tmp/oi-project', CURRENT_TIMESTAMP)`)
			insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'oi-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
			mustExec(t, db, insert, "existing-hermes", 1, "hermes")
			if legacyQM {
				mustExec(t, db, insert, "existing-qm", 2, "qm")
			}
			upTo(t, db, openInterpreterMigrationVersion)
			if _, err := db.Exec(insert, "oi-session", 3, "open-interpreter"); err != nil {
				t.Fatalf("insert open-interpreter session after migration: %v", err)
			}
			var applied int
			if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, openInterpreterMigrationVersion).Scan(&applied); err != nil || applied != 1 {
				t.Fatalf("migration receipts = %d, err = %v; want 1", applied, err)
			}
			if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			gooseMu.Lock()
			rollbackErr := goose.DownTo(db, "migrations", beforeOpenInterpreterMigration)
			gooseMu.Unlock()
			if rollbackErr == nil {
				t.Fatal("rollback accepted existing open-interpreter session")
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'open-interpreter'`)
			downTo(t, db, beforeOpenInterpreterMigration)
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("down migration did not restore original schema: %v", err)
			}
			if _, err := db.Exec(insert, "oi-after-down", 5, "open-interpreter"); err == nil || !strings.Contains(err.Error(), "CHECK") {
				t.Fatalf("open-interpreter insertion after downgrade = %v; want CHECK failure", err)
			}
			var integrity string
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
			}
		})
	}
}

func TestMigration20261116125847855RejectsUnrecognizedConstraint(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeOpenInterpreterMigration)
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
	err := goose.UpTo(db, "migrations", openInterpreterMigrationVersion)
	gooseMu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("unrecognized constraint silently migrated: %v", err)
	}
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, openInterpreterMigrationVersion).Scan(&applied); err != nil || applied != 0 {
		t.Fatalf("failed migration marked applied: count = %d, err = %v", applied, err)
	}
}

// Draft builds recorded Open Interpreter's constraint under 0199, 0207, 0216
// or 20261114174124485. Those databases already allow the harness; the
// migration must apply once without duplicating it, and startup must stay
// repeatable.
func TestMigration20261116125847855AdoptsDraftOpenInterpreterHistory(t *testing.T) {
	for _, draftVersion := range []int64{207, 216, 20261114174124485} {
		t.Run(fmt.Sprintf("draft_%04d", draftVersion), func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, beforeOpenInterpreterMigration)
			mustExec(t, db, `PRAGMA writable_schema = ON`)
			mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''open-interpreter'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
			mustExec(t, db, `PRAGMA writable_schema = RESET`)
			mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, draftVersion)
			for range 2 {
				if err := migrate(db); err != nil {
					t.Fatalf("migrate draft %d history: %v", draftVersion, err)
				}
			}
			var schema string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
				t.Fatal(err)
			}
			if count := strings.Count(schema, "'open-interpreter'"); count != 1 {
				t.Fatalf("open-interpreter constraint entries = %d; want 1", count)
			}
			var applied int
			if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, openInterpreterMigrationVersion).Scan(&applied); err != nil || applied != 1 {
				t.Fatalf("migration receipts = %d, err = %v; want 1", applied, err)
			}
		})
	}
}

func TestMigration20261116125847855RepairsDraft0199History(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 198)
	// The earliest draft builds used 0199 for the Open Interpreter constraint.
	// Main uses it for conversation_messages.continuation, which goose skips on
	// such a database; startup reconciliation adds the column.
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (199, 1)`)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate draft history: %v", err)
	}
	var continuationColumn int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('conversation_messages') WHERE name = 'continuation'`).Scan(&continuationColumn); err != nil || continuationColumn != 1 {
		t.Fatalf("conversation_messages.continuation count = %d, err = %v; want 1", continuationColumn, err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil || !strings.Contains(schema, "'open-interpreter'") {
		t.Fatalf("sessions schema after draft history = %q, err = %v", schema, err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("repeat migrate draft history: %v", err)
	}
}

func TestMigration20261116125847855CoexistsWithCodeBuddy(t *testing.T) {
	for _, draftOpenInterpreter := range []bool{false, true} {
		name := "codebuddy_0207"
		if draftOpenInterpreter {
			name = "codebuddy_and_draft_open_interpreter_0207"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, beforeOpenInterpreterMigration)
			// CodeBuddy's old 0207 draft migration or startup repair adds its harness.
			// Some draft databases also recorded Open Interpreter at 0207.
			harnessTail := "'codebuddy', 'fake'))"
			if draftOpenInterpreter {
				harnessTail = "'codebuddy', 'open-interpreter', 'fake'))"
			}
			mustExec(t, db, `PRAGMA writable_schema = ON`)
			mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', ?) WHERE type = 'table' AND name = 'sessions'`, harnessTail)
			mustExec(t, db, `PRAGMA writable_schema = RESET`)
			mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (207, 1)`)
			for range 2 {
				if err := migrate(db); err != nil {
					t.Fatalf("migrate CodeBuddy and Open Interpreter history: %v", err)
				}
			}
			var schema string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
				t.Fatal(err)
			}
			for _, harness := range []string{"codebuddy", "open-interpreter", "hermes"} {
				if count := strings.Count(schema, "'"+harness+"'"); count != 1 {
					t.Fatalf("%s constraint entries = %d; want 1", harness, count)
				}
			}
			mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('coexist-project', '/tmp/coexist-project', CURRENT_TIMESTAMP)`)
			insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'coexist-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
			mustExec(t, db, insert, "codebuddy-session", 1, "codebuddy")
			mustExec(t, db, insert, "open-interpreter-session", 2, "open-interpreter")
		})
	}
}

// Startup reconciliation removes Open Interpreter before exact historical
// repairs, like Hermes, then re-adds it once without dropping later harnesses.
func TestOpenInterpreterLegacyRepairPreservesLaterHarness(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeOpenInterpreterMigration)
	upTo(t, db, openInterpreterMigrationVersion)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(replace(sql, '''command-code'', ', ''), '''fake''))', '''later-harness'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	for range 2 {
		if err := reconcileHarnessConstraint(db); err != nil {
			t.Fatalf("legacy repair with Open Interpreter present: %v", err)
		}
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"'open-interpreter'", "'hermes'", "'command-code'", "'later-harness'"} {
		if strings.Count(schema, harness) != 1 {
			t.Fatalf("legacy repair lost or duplicated %s: %s", harness, schema)
		}
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('later-project', '/tmp/later-project', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'later-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	for i, harness := range []string{"open-interpreter", "hermes", "command-code", "later-harness"} {
		mustExec(t, db, insert, harness, i+1, harness)
	}
}
