package sqlite

import (
	"strings"
	"testing"
)

// TestMigration0216AllowsOpenInterpreterAndReversesBothHistoricalSchemas mirrors the
// fx and DeepSeek coverage: the new harness must be insertable on a current and a
// legacy-qm schema, an unknown harness must still be rejected, and the down
// migration must restore the exact prior constraint.
func TestMigration0216AllowsOpenInterpreterAndReversesBothHistoricalSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 206)
			if legacyQM {
				// The retained legacy 'qm' fixture harness sits before
				// 'codewhale' in every variant 0216 rewrites, so anchor
				// there to produce a schema the migration still recognizes.
				mustExec(t, db, `PRAGMA writable_schema = ON`)
				mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''openhands'', ''codewhale''', '''openhands'', ''qm'', ''codewhale''') WHERE type = 'table' AND name = 'sessions'`)
				mustExec(t, db, `PRAGMA writable_schema = RESET`)
			}
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
			upTo(t, db, 216)
			if _, err := db.Exec(insert, "cc-session", 3, "open-interpreter"); err != nil {
				t.Fatalf("insert open-interpreter session after migration: %v", err)
			}
			var version int
			if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != 216 {
				t.Fatalf("migration version = %d, err = %v; want 216", version, err)
			}
			if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'open-interpreter'`)
			downTo(t, db, 206)
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("down migration did not restore original schema: %v", err)
			}
			if _, err := db.Exec(insert, "cc-after-down", 5, "open-interpreter"); err == nil || !strings.Contains(err.Error(), "CHECK") {
				t.Fatalf("open-interpreter insertion after downgrade = %v; want CHECK failure", err)
			}
			var integrity string
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
			}
		})
	}
}

func TestMigration0216RepairsDraft0199History(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 198)
	// Earlier draft builds used 0199 for the Open Interpreter constraint. Main
	// now uses it for conversation_messages.continuation, which goose otherwise
	// skips on an existing draft database.
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (199, 1)`)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate draft history: %v", err)
	}
	var continuationColumn int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('conversation_messages') WHERE name = 'continuation'`).Scan(&continuationColumn); err != nil || continuationColumn != 1 {
		t.Fatalf("conversation_messages.continuation count = %d, err = %v; want 1", continuationColumn, err)
	}
	var sessionsSchema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&sessionsSchema); err != nil || !strings.Contains(sessionsSchema, "'open-interpreter'") {
		t.Fatalf("sessions schema after draft history = %q, err = %v", sessionsSchema, err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("repeat migrate draft history: %v", err)
	}
}

func TestMigration0216PreservesDraft0207History(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 206)
	// Previous draft builds recorded Open Interpreter's constraint at 0207.
	// Renumbering must leave that schema usable and apply 0216 only once.
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''open-interpreter'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (207, 1)`)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate draft 0207 history: %v", err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(schema, "'open-interpreter'"); count != 1 {
		t.Fatalf("open-interpreter constraint entries = %d; want 1", count)
	}
	var version int
	if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != 216 {
		t.Fatalf("migration version = %d, err = %v; want 216", version, err)
	}
}

func TestMigration0216CoexistsWithCodeBuddy(t *testing.T) {
	for _, draftOpenInterpreter := range []bool{false, true} {
		name := "codebuddy_0207"
		if draftOpenInterpreter {
			name = "codebuddy_and_draft_open_interpreter_0207"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 206)
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
			for _, harness := range []string{"codebuddy", "open-interpreter"} {
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
