package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	tauMigrationVersion       int64 = 20261117090000000
	tauMigrationPriorVersion  int64 = 20261116231134450
	tauMigrationSessionInsert       = `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'tau-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
)

// TestMigration20261117090000000AllowsTauAndReversesBothHistoricalSchemas mirrors the
// Hermes coverage: the new harness must be insertable on a current and a
// legacy-qm schema, an unknown harness must still be rejected, a rollback must
// refuse existing Tau rows, and the down migration must restore the exact prior
// constraint.
func TestMigration20261117090000000AllowsTauAndReversesBothHistoricalSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, tauMigrationPriorVersion)
			if legacyQM {
				// The retained legacy 'qm' fixture harness sits before
				// 'codewhale' in every historical variant, so anchor there to
				// produce a schema the migration still recognizes.
				mustExec(t, db, `PRAGMA writable_schema = ON`)
				mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''openhands'', ''codewhale''', '''openhands'', ''qm'', ''codewhale''') WHERE type = 'table' AND name = 'sessions'`)
				mustExec(t, db, `PRAGMA writable_schema = RESET`)
			}
			var before string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('tau-project', '/tmp/tau-project', CURRENT_TIMESTAMP)`)
			mustExec(t, db, tauMigrationSessionInsert, "existing-hermes", 1, "hermes")
			if legacyQM {
				mustExec(t, db, tauMigrationSessionInsert, "existing-qm", 2, "qm")
			}
			upTo(t, db, tauMigrationVersion)
			if _, err := db.Exec(tauMigrationSessionInsert, "tau-session", 3, "tau"); err != nil {
				t.Fatalf("insert tau session after migration: %v", err)
			}
			var version int64
			if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != tauMigrationVersion {
				t.Fatalf("migration version = %d, err = %v; want %d", version, err, tauMigrationVersion)
			}
			if _, err := db.Exec(tauMigrationSessionInsert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			gooseMu.Lock()
			rollbackErr := goose.DownTo(db, "migrations", tauMigrationPriorVersion)
			gooseMu.Unlock()
			if rollbackErr == nil {
				t.Fatal("rollback accepted existing tau session")
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'tau'`)
			downTo(t, db, tauMigrationPriorVersion)
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("down migration did not restore original schema: %v", err)
			}
			if _, err := db.Exec(tauMigrationSessionInsert, "tau-after-down", 5, "tau"); err == nil || !strings.Contains(err.Error(), "CHECK") {
				t.Fatalf("tau insertion after downgrade = %v; want CHECK failure", err)
			}
			var integrity string
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
			}
		})
	}
}

func TestMigration20261117090000000RejectsUnrecognizedConstraint(t *testing.T) {
	db := openMigratedDatabaseCopy(t, tauMigrationPriorVersion)
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
	err := goose.UpTo(db, "migrations", tauMigrationVersion)
	gooseMu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("unrecognized constraint silently migrated: %v", err)
	}
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, tauMigrationVersion).Scan(&applied); err != nil || applied != 0 {
		t.Fatalf("failed migration marked applied: count = %d, err = %v", applied, err)
	}
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake'', ''other''))', '''fake''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	upTo(t, db, tauMigrationVersion)
}

func TestMigration20261117090000000PreservesHarnessesAfterTau(t *testing.T) {
	db := openMigratedDatabaseCopy(t, tauMigrationPriorVersion)
	upTo(t, db, tauMigrationVersion)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''later-harness'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// A later harness may follow Tau; rollback must remove only Tau.
	downTo(t, db, tauMigrationPriorVersion)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(before, "'tau', ", "", 1); after != want {
		t.Fatalf("rollback changed entries other than Tau:\ngot: %s\nwant: %s", after, want)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('tau-project', '/tmp/tau-project', CURRENT_TIMESTAMP)`)
	mustExec(t, db, tauMigrationSessionInsert, "later", 1, "later-harness")
	if _, err := db.Exec(tauMigrationSessionInsert, "tau", 2, "tau"); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("Tau insertion after rollback = %v; want CHECK failure", err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after rollback = %q, %v", integrity, err)
	}
}

// TestTauRepairAnchorMatchesEveryHistoricalHarnessList guards the anchor the
// schema repair relies on for this harness: whatever schema a database arrives
// from, the sessions harness CHECK must end with the retained 'fake' fixture
// harness, otherwise the repair would silently skip it and a database that
// missed an earlier harness migration could never accept this harness.
func TestTauRepairAnchorMatchesEveryHistoricalHarnessList(t *testing.T) {
	for _, version := range []int64{26, 53, 54, 82, 95, 155, 163, 164, 165, 166, 167, 168, 174, 194, 215, tauMigrationPriorVersion} {
		db := openMigratedDatabaseCopy(t, version)
		var sql string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&sql); err != nil {
			t.Fatal(err)
		}
		start := strings.Index(sql, "CHECK (harness IN")
		if start < 0 {
			t.Fatalf("schema at %d has no harness CHECK", version)
		}
		end := strings.Index(sql[start:], "))")
		if end < 0 {
			t.Fatalf("schema at %d has an unterminated harness CHECK", version)
		}
		check := sql[start : start+end+2]
		if !strings.HasSuffix(check, `'fake'))`) {
			t.Fatalf("schema at %d ends its harness CHECK with %q; the Tau repair anchor expects 'fake'))", version, check)
		}
	}
}

// TestReconcileTauAfterBurnedMigration covers a database that never ran
// an earlier harness migration: goose skips the burned number and the repair
// must still add Tau while leaving every earlier harness intact, which is the
// failure mode when a migration widens a schema variant the repair also relies on.
func TestReconcileTauAfterBurnedMigration(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 164)
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (165, 1)`); err != nil {
		t.Fatalf("seed burned MiMo migration: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate burned profile: %v", err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"'fx'", "'gemini'", "'unreal-agent'", "'mimo-code'", "'deepseek-harness'", "'openhands'", "'hermes'", "'tau'"} {
		if strings.Count(schema, harness) != 1 {
			t.Fatalf("repaired sessions constraint lost or duplicated %s: %s", harness, schema)
		}
	}
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatalf("repeat repair: %v", err)
	}
}
