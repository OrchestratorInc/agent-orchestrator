package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// zcodeMigrationVersion is the timestamped version that widens the sessions
// harness CHECK for ZCode.
const zcodeMigrationVersion int64 = 20261028122414418

// priorZCodeMigrationVersion is the newest shipped migration before ZCode's.
const priorZCodeMigrationVersion int64 = 20261022172135175

// TestZCodeMigrationAllowsZCodeAndReversesHistoricSchemas mirrors the Hermes
// and Memcode coverage: the new harness must be insertable on a current and a
// legacy-qm schema, an unknown harness must still be rejected, and the down
// migration must restore the exact prior constraint while refusing to run with
// ZCode rows present.
func TestZCodeMigrationAllowsZCodeAndReversesHistoricSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, priorZCodeMigrationVersion)
			if legacyQM {
				mustExec(t, db, `PRAGMA writable_schema = ON`)
				mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''openhands'', ''codewhale''', '''openhands'', ''qm'', ''codewhale''') WHERE type = 'table' AND name = 'sessions'`)
				mustExec(t, db, `PRAGMA writable_schema = RESET`)
			}
			upTo(t, db, priorZCodeMigrationVersion)
			var before string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('zcode-project', '/tmp/zcode-project', CURRENT_TIMESTAMP)`)
			insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'zcode-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
			mustExec(t, db, insert, "existing-omp", 1, "omp")
			if legacyQM {
				mustExec(t, db, insert, "existing-qm", 2, "qm")
			}
			upTo(t, db, zcodeMigrationVersion)
			if _, err := db.Exec(insert, "zcode-session", 3, "zcode"); err != nil {
				t.Fatalf("insert zcode session after migration: %v", err)
			}
			var version int64
			if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != zcodeMigrationVersion {
				t.Fatalf("migration version = %d, err = %v; want %d", version, err, zcodeMigrationVersion)
			}
			if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			gooseMu.Lock()
			rollbackErr := goose.DownTo(db, "migrations", priorZCodeMigrationVersion)
			gooseMu.Unlock()
			if rollbackErr == nil {
				t.Fatal("rollback accepted an existing zcode session")
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'zcode'`)
			downTo(t, db, priorZCodeMigrationVersion)
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("down migration did not restore original schema: %v", err)
			}
			if _, err := db.Exec(insert, "zcode-after-down", 5, "zcode"); err == nil || !strings.Contains(err.Error(), "CHECK") {
				t.Fatalf("zcode insertion after downgrade = %v; want CHECK failure", err)
			}
			var integrity string
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
			}
		})
	}
}

// TestZCodeReconcileRepairsSkippedMigration simulates a database whose goose
// history contains the ZCode version but whose sessions constraint lost the
// harness (the collapsed-migration silent-no-op failure mode): reconcile must
// re-add ZCode so sessions can store it.
func TestZCodeReconcileRepairsSkippedMigration(t *testing.T) {
	db := openMigratedDatabaseCopy(t, zcodeMigrationVersion)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''zcode'', ', '') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)

	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatalf("repair: %v", err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "'zcode'") {
		t.Fatalf("repaired constraint is still missing ZCode:\n%s", schema)
	}
}
