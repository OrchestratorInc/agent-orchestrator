package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestPoolMigrationAllowsPoolAndReversesBothHistoricalSchemas mirrors the
// fx and DeepSeek coverage: the new harness must be insertable on a current and a
// legacy-qm schema, an unknown harness must still be rejected, and the down
// migration must restore the exact prior constraint.
func TestPoolMigrationAllowsPoolAndReversesBothHistoricalSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 20261114072016988)
			if legacyQM {
				// The retained legacy 'qm' fixture harness sits before
				// 'codewhale' in every variant the Pool migration rewrites, so anchor
				// there to produce a schema the migration still recognizes.
				mustExec(t, db, `PRAGMA writable_schema = ON`)
				mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''openhands'', ''codewhale''', '''openhands'', ''qm'', ''codewhale''') WHERE type = 'table' AND name = 'sessions'`)
				mustExec(t, db, `PRAGMA writable_schema = RESET`)
			}
			upTo(t, db, 20261114072016988)
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
			upTo(t, db, 20261114174351283)
			if _, err := db.Exec(insert, "cc-session", 3, "pool"); err != nil {
				t.Fatalf("insert pool session after migration: %v", err)
			}
			var version int
			if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != 20261114174351283 {
				t.Fatalf("migration version = %d, err = %v; want 20261114174351283", version, err)
			}
			if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			// A rollback must refuse while a Pool row would violate the restored CHECK.
			gooseMu.Lock()
			rollbackErr := goose.DownTo(db, "migrations", 20261114072016988)
			gooseMu.Unlock()
			if rollbackErr == nil || !strings.Contains(rollbackErr.Error(), "CHECK constraint failed") {
				t.Fatalf("rollback with an existing pool session = %v; want guard refusal", rollbackErr)
			}
			var refused string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&refused); err != nil || !strings.Contains(refused, "'pool'") {
				t.Fatalf("refused rollback changed the constraint: %v", err)
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'pool'`)
			downTo(t, db, 20261114072016988)
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("down migration did not restore original schema: %v", err)
			}
			if _, err := db.Exec(insert, "cc-after-down", 5, "pool"); err == nil || !strings.Contains(err.Error(), "CHECK") {
				t.Fatalf("pool insertion after downgrade = %v; want CHECK failure", err)
			}
			var integrity string
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
			}
		})
	}
}

// TestPoolRepairAnchorMatchesEveryHistoricalList guards the anchor the
// schema repair relies on for this harness: whatever schema a database arrives
// from, the sessions harness CHECK must end with the retained 'fake' fixture
// harness, otherwise the repair would silently skip it and a database that
// missed an earlier harness migration could never accept this harness.
func TestPoolRepairAnchorMatchesEveryHistoricalHarnessList(t *testing.T) {
	for _, version := range []int64{26, 53, 54, 82, 95, 155, 163, 164, 165, 166, 167, 168, 174, 20261019220421514, 20261114072016988} {
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
			t.Fatalf("schema at %d ends its harness CHECK with %q; the Pool repair anchor expects 'fake'))", version, check)
		}
		if !strings.Contains(check, `'fake'`) {
			t.Fatalf("schema at %d has no fake harness entry: %q", version, check)
		}
	}
}

// TestPoolMigrationDownRemovesOnlyPoolAfterLaterHarness covers the next
// harness migration: it inserts its entry before 'fake', so Pool no longer
// precedes 'fake'. The rollback must still remove Pool, and only Pool.
func TestPoolMigrationDownRemovesOnlyPoolAfterLaterHarness(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261114072016988)
	upTo(t, db, 20261114174351283)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''later-harness'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before, "'pool', 'later-harness', 'fake'))") {
		t.Fatalf("fixture did not place a later harness between Pool and fake: %s", before)
	}
	downTo(t, db, 20261114072016988)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(before, "'pool', ", "", 1); after != want {
		t.Fatalf("rollback changed entries other than Pool:\ngot: %s\nwant: %s", after, want)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('later-project', '/tmp/later-project', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'later-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	mustExec(t, db, insert, "later", 1, "later-harness")
	if _, err := db.Exec(insert, "pool", 2, "pool"); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("pool insertion after rollback = %v; want CHECK failure", err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after rollback = %q, %v", integrity, err)
	}
}

// TestPoolMigrationRejectsUnrecognizedConstraint asserts the Up guard: a
// constraint the anchor cannot widen fails the migration instead of being
// recorded as applied without Pool.
func TestPoolMigrationRejectsUnrecognizedConstraint(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261114072016988)
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
	err := goose.UpTo(db, "migrations", 20261114174351283)
	gooseMu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("unrecognized constraint silently migrated: %v", err)
	}
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 20261114174351283 AND is_applied = 1`).Scan(&applied); err != nil || applied != 0 {
		t.Fatalf("failed migration marked applied: count = %d, err = %v", applied, err)
	}
}

// TestReconcilePoolAfterBurnedMigration covers a database that never ran
// an earlier harness migration: goose skips the burned number, the Pool
// migration still widens the historical constraint, and the startup repair
// sets Pool aside so its exact historical repairs match before restoring it.
// It must leave every earlier harness intact, which is the failure mode when
// a migration widens a schema variant the repair also relies on.
func TestReconcilePoolAfterBurnedMigration(t *testing.T) {
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
	for _, harness := range []string{"'fx'", "'gemini'", "'unreal-agent'", "'mimo-code'", "'deepseek-harness'", "'openhands'", "'command-code'", "'hermes'", "'pool'"} {
		if !strings.Contains(schema, harness) {
			t.Fatalf("repaired sessions constraint lost %s: %s", harness, schema)
		}
	}
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatalf("repeat repair: %v", err)
	}
}
