package sqlite

import (
	"strings"
	"testing"
)

// TestMigration0209AllowsNeovateAndReversesBothHistoricalSchemas mirrors the
// fx and DeepSeek coverage: the new harness must be insertable on a current and a
// legacy-qm schema, an unknown harness must still be rejected, and the down
// migration must restore the exact prior constraint.
func TestMigration0209AllowsNeovateAndReversesBothHistoricalSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 195)
			if legacyQM {
				// The retained legacy 'qm' fixture harness sits before
				// 'codewhale' in every variant 0209 rewrites, so anchor
				// there to produce a schema the migration still recognizes.
				mustExec(t, db, `PRAGMA writable_schema = ON`)
				mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''openhands'', ''codewhale''', '''openhands'', ''qm'', ''codewhale''') WHERE type = 'table' AND name = 'sessions'`)
				mustExec(t, db, `PRAGMA writable_schema = RESET`)
			}
			upTo(t, db, 195)
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
			upTo(t, db, 209)
			if _, err := db.Exec(insert, "cc-session", 3, "neovate"); err != nil {
				t.Fatalf("insert neovate session after migration: %v", err)
			}
			var version int
			if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != 209 {
				t.Fatalf("migration version = %d, err = %v; want 209", version, err)
			}
			if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'neovate'`)
			downTo(t, db, 195)
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("down migration did not restore original schema: %v", err)
			}
			if _, err := db.Exec(insert, "cc-after-down", 5, "neovate"); err == nil || !strings.Contains(err.Error(), "CHECK") {
				t.Fatalf("neovate insertion after downgrade = %v; want CHECK failure", err)
			}
			var integrity string
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
			}
		})
	}
}

// Old Neovate builds recorded 195 without main's requested_output column.
// Cover both an old native constraint and an incomplete burned schema.
func TestNeovateMigrationRepairsBurnedRequestedOutput(t *testing.T) {
	for _, oldHarness := range []bool{false, true} {
		t.Run(map[bool]string{false: "incomplete", true: "old_neovate"}[oldHarness], func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 194)
			if oldHarness {
				mustExec(t, db, `PRAGMA writable_schema = ON`)
				mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''neovate'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
				mustExec(t, db, `PRAGMA writable_schema = RESET`)
			}
			mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (195, 1)`)
			if err := migrate(db); err != nil {
				t.Fatalf("migrate burned Neovate: %v", err)
			}
			var columns int
			if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'requested_output'`).Scan(&columns); err != nil || columns != 1 {
				t.Fatalf("requested_output columns=%d err=%v", columns, err)
			}
			mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('nv', '/tmp/nv', CURRENT_TIMESTAMP)`)
			mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, requested_output, created_at, updated_at, activity_last_at) VALUES ('nv-1', 'nv', 1, 'neovate', 'retain-output', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
			if err := migrate(db); err != nil {
				t.Fatalf("repeat migrate: %v", err)
			}
			var output string
			if err := db.QueryRow(`SELECT requested_output FROM sessions WHERE id = 'nv-1'`).Scan(&output); err != nil || output != "retain-output" {
				t.Fatalf("requested output=%q err=%v", output, err)
			}
		})
	}
}

func TestNeovateMigrationPreservesMainRequestedOutput(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 195)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('nv', '/tmp/nv', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, requested_output, created_at, updated_at, activity_last_at) VALUES ('nv-1', 'nv', 1, 'claude-code', 'existing-output', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate current main: %v", err)
	}
	var output string
	if err := db.QueryRow(`SELECT requested_output FROM sessions WHERE id = 'nv-1'`).Scan(&output); err != nil || output != "existing-output" {
		t.Fatalf("requested output=%q err=%v", output, err)
	}
}
