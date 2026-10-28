package sqlite

import (
	"strings"
	"testing"
)

// TestMigrationStrandsAllowsStrandsAndReversesBothHistoricalSchemas mirrors the
// fx and DeepSeek coverage: the new harness must be insertable on a current and a
// legacy-qm schema, an unknown harness must still be rejected, and the down
// migration must restore the exact prior constraint.
func TestMigrationStrandsAllowsStrandsAndReversesBothHistoricalSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 20261024194546592)
			if legacyQM {
				// The retained legacy 'qm' fixture harness sits before
				// 'codewhale' in every variant the Strands migration rewrites, so anchor
				// there to produce a schema the migration still recognizes.
				mustExec(t, db, `PRAGMA writable_schema = ON`)
				mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''openhands'', ''codewhale''', '''openhands'', ''qm'', ''codewhale''') WHERE type = 'table' AND name = 'sessions'`)
				mustExec(t, db, `PRAGMA writable_schema = RESET`)
			}
			upTo(t, db, 20261024194546592)
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
			upTo(t, db, 20261028082940187)
			if _, err := db.Exec(insert, "cc-session", 3, "strands"); err != nil {
				t.Fatalf("insert strands session after migration: %v", err)
			}
			var version int64
			if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != 20261028082940187 {
				t.Fatalf("migration version = %d, err = %v; want 20261028082940187", version, err)
			}
			if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'strands'`)
			downTo(t, db, 20261024194546592)
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("down migration did not restore original schema: %v", err)
			}
			if _, err := db.Exec(insert, "cc-after-down", 5, "strands"); err == nil || !strings.Contains(err.Error(), "CHECK") {
				t.Fatalf("strands insertion after downgrade = %v; want CHECK failure", err)
			}
			var integrity string
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
			}
		})
	}
}

func TestReconcileStrandsRejectsDivergentHarnessConstraint(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261024194546592)
	// Start from a valid current constraint so another harness added by main
	// cannot mask the Strands-specific negative fixture.
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''strands'', ', '') WHERE type = 'table' AND name = 'sessions'`)
	// Keep existing Hermes outside its exact whitespace normalization so the
	// deliberately malformed tail specifically exercises the Strands guard.
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, ?, ?) WHERE type = 'table' AND name = 'sessions'`, "'hermes', ", "'hermes',")
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''fake'' ))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)

	err := reconcileHarnessConstraint(db)
	if err == nil || !strings.Contains(err.Error(), "missing Strands") {
		t.Fatalf("reconcile divergent sessions constraint = %v; want missing Strands error", err)
	}
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(after, "'strands'") {
		t.Fatal("divergent constraint unexpectedly admitted Strands")
	}
}
