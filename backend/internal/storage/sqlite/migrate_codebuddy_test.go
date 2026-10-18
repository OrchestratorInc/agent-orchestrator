package sqlite

import (
	"strings"
	"testing"
)

// TestMigration0207AllowsCodeBuddyAndReversesBothHistoricalSchemas mirrors the
// fx and DeepSeek coverage: the new harness must be insertable on a current and a
// legacy-qm schema, an unknown harness must still be rejected, and the down
// migration must restore the exact prior constraint.
func TestMigration0207AllowsCodeBuddyAndReversesBothHistoricalSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 194)
			if legacyQM {
				// The retained legacy 'qm' fixture harness sits before
				// 'codewhale' in every variant 0207 rewrites, so anchor
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
			upTo(t, db, 207)
			if _, err := db.Exec(insert, "cc-session", 3, "codebuddy"); err != nil {
				t.Fatalf("insert codebuddy session after migration: %v", err)
			}
			var version int
			if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != 207 {
				t.Fatalf("migration version = %d, err = %v; want 207", version, err)
			}
			if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'codebuddy'`)
			downTo(t, db, 194)
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("down migration did not restore original schema: %v", err)
			}
			if _, err := db.Exec(insert, "cc-after-down", 5, "codebuddy"); err == nil || !strings.Contains(err.Error(), "CHECK") {
				t.Fatalf("codebuddy insertion after downgrade = %v; want CHECK failure", err)
			}
			var integrity string
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
			}
		})
	}
}

// TestReconcileCodeBuddyAfterBurnedMigration covers a database that never ran
// an earlier harness migration: goose skips the burned number, the current-schema
// rewrite in 0207 matches nothing, and the repair is the only thing that adds
// CodeBuddy Code. It must also leave every earlier harness intact, which is the
// failure mode when a migration widens a schema variant the repair also relies on.
func TestReconcileCodeBuddyAfterBurnedMigration(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 164)
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (165, 1)`); err != nil {
		t.Fatalf("seed burned MiMo migration: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (207, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate burned profile: %v", err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"'fx'", "'gemini'", "'unreal-agent'", "'mimo-code'", "'deepseek-harness'", "'openhands'", "'command-code'", "'memcode'", "'codebuddy'"} {
		if !strings.Contains(schema, harness) {
			t.Fatalf("repaired sessions constraint lost %s: %s", harness, schema)
		}
	}
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatalf("repeat repair: %v", err)
	}
}
