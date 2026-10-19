package sqlite

import (
	"fmt"
	"strings"
	"testing"
)

// TestCodeBuddyMigrationAllowsCodeBuddyAndReversesBothHistoricalSchemas mirrors the
// fx and DeepSeek coverage: the new harness must be insertable on a current and a
// legacy-qm schema, an unknown harness must still be rejected, and the down
// migration must restore the exact prior constraint.
func TestCodeBuddyMigrationAllowsCodeBuddyAndReversesBothHistoricalSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 215)
			if legacyQM {
				// The retained legacy 'qm' fixture harness sits before
				// 'codewhale' in every variant the CodeBuddy migration rewrites, so anchor
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
			upTo(t, db, 20261019155340715)
			if _, err := db.Exec(insert, "cc-session", 3, "codebuddy"); err != nil {
				t.Fatalf("insert codebuddy session after migration: %v", err)
			}
			var version int
			if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != 20261019155340715 {
				t.Fatalf("migration version = %d, err = %v; want 20261019155340715", version, err)
			}
			if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'codebuddy'`)
			downTo(t, db, 215)
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

// A draft Open Interpreter build used 0207 before it was renumbered. That
// recorded version must not hide CodeBuddy's later migration, and both harness
// values must remain usable after opening the database with this build.
func TestCodeBuddyMigrationAfterOpenInterpreterDraft0207(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 215)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''open-interpreter'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (207, 1)`)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate Open Interpreter draft profile: %v", err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"'open-interpreter'", "'memcode'", "'codebuddy'"} {
		if !strings.Contains(schema, harness) {
			t.Fatalf("migration lost %s: %s", harness, schema)
		}
	}
	if strings.Count(schema, "'codebuddy'") != 1 {
		t.Fatalf("CodeBuddy appears more than once in sessions constraint: %s", schema)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('cc-draft-project', '/tmp/cc-draft-project', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'cc-draft-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	mustExec(t, db, insert, "oi-draft", 1, "open-interpreter")
	mustExec(t, db, insert, "cc-after-draft", 2, "codebuddy")
}

// TestReconcileCodeBuddyAfterBurnedMigration covers a database that never ran
// an earlier harness migration: goose skips the burned number, the current-schema
// CodeBuddy rewrite matches nothing, and the repair is the only thing that adds
// CodeBuddy Code. It must also leave every earlier harness intact, which is the
// failure mode when a migration widens a schema variant the repair also relies on.
func TestReconcileCodeBuddyAfterBurnedMigration(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 164)
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (165, 1)`); err != nil {
		t.Fatalf("seed burned MiMo migration: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (20261019155340715, 1)`); err != nil {
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

// Earlier published draft branches used both numbers. Releasing the collided
// ledger entry must restore main's schema without losing an existing worker.
func TestCodeBuddyPreviewMigrationCollisionsPreserveSessions(t *testing.T) {
	for _, version := range []int64{214, 215} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 212)
			mustExec(t, db, `PRAGMA writable_schema = ON`)
			mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''codebuddy'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
			mustExec(t, db, `PRAGMA writable_schema = RESET`)
			mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, version)
			mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('preview', '/preview', CURRENT_TIMESTAMP)`)
			mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, activity_last_at, created_at, updated_at) VALUES ('existing-codebuddy', 'preview', 1, 'codebuddy', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
			for attempt := range 2 {
				if err := migrate(db); err != nil {
					t.Fatalf("migrate attempt %d: %v", attempt, err)
				}
				var objects int
				if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE (type = 'table' AND name IN ('managed_harness_releases', 'session_harness_releases')) OR (type = 'trigger' AND name IN ('sessions_record_harness_release_insert', 'sessions_record_harness_release_update'))`).Scan(&objects); err != nil || objects != 4 {
					t.Fatalf("main release schema objects = %d: %v", objects, err)
				}
				var refresh bool
				if err := db.QueryRow(`SELECT version_refresh FROM app_settings WHERE id = 1`).Scan(&refresh); err != nil || !refresh {
					t.Fatalf("main version refresh missing: %v %v", refresh, err)
				}
				var harness string
				if err := db.QueryRow(`SELECT harness FROM sessions WHERE id = 'existing-codebuddy'`).Scan(&harness); err != nil || harness != "codebuddy" {
					t.Fatalf("preview session changed: %q %v", harness, err)
				}
			}
		})
	}
}

func TestCodeBuddyPreviewRepairRefusesPartialCanonicalSchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 212)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''codebuddy'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (214, 1), (215, 1)`)
	mustExec(t, db, `CREATE TABLE managed_harness_releases (harness TEXT PRIMARY KEY)`)
	if err := repairRenumberedCodeBuddyMigrationHistory(db); err == nil || !strings.Contains(err.Error(), "partial schema") {
		t.Fatalf("partial canonical schema accepted: %v", err)
	}
	var retained int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id IN (214, 215) AND is_applied = 1`).Scan(&retained); err != nil || retained != 2 {
		t.Fatalf("partial repair changed ledger: %d %v", retained, err)
	}
}
