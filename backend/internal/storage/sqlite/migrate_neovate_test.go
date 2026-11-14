package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	neovateMigration       = 20261114173957269
	beforeNeovateMigration = 20261114072016988
)

// TestMigration20261114173957269AllowsNeovateAndReversesBothHistoricalSchemas
// mirrors the Hermes coverage: the new harness must be insertable on a current
// and a legacy-qm schema, an unknown harness must still be rejected, and the
// down migration must restore the exact prior constraint.
func TestMigration20261114173957269AllowsNeovateAndReversesBothHistoricalSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, beforeNeovateMigration)
			if legacyQM {
				// The retained legacy 'qm' fixture harness sits before
				// 'codewhale' in every variant this migration rewrites, so
				// anchor there to produce a schema the migration still recognizes.
				mustExec(t, db, `PRAGMA writable_schema = ON`)
				mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''openhands'', ''codewhale''', '''openhands'', ''qm'', ''codewhale''') WHERE type = 'table' AND name = 'sessions'`)
				mustExec(t, db, `PRAGMA writable_schema = RESET`)
			}
			var before string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('nv-project', '/tmp/nv-project', CURRENT_TIMESTAMP)`)
			insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'nv-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
			mustExec(t, db, insert, "existing-omp", 1, "omp")
			if legacyQM {
				mustExec(t, db, insert, "existing-qm", 2, "qm")
			}
			upTo(t, db, neovateMigration)
			if _, err := db.Exec(insert, "nv-session", 3, "neovate"); err != nil {
				t.Fatalf("insert neovate session after migration: %v", err)
			}
			var version int64
			if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != neovateMigration {
				t.Fatalf("migration version = %d, err = %v; want %d", version, err, neovateMigration)
			}
			if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			gooseMu.Lock()
			rollbackErr := goose.DownTo(db, "migrations", beforeNeovateMigration)
			gooseMu.Unlock()
			if rollbackErr == nil {
				t.Fatal("rollback accepted existing neovate session")
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'neovate'`)
			downTo(t, db, beforeNeovateMigration)
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("down migration did not restore original schema: %v", err)
			}
			if _, err := db.Exec(insert, "nv-after-down", 5, "neovate"); err == nil || !strings.Contains(err.Error(), "CHECK") {
				t.Fatalf("neovate insertion after downgrade = %v; want CHECK failure", err)
			}
			var integrity string
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
			}
		})
	}
}

func TestMigration20261114173957269RejectsUnrecognizedConstraint(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeNeovateMigration)
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
	err := goose.UpTo(db, "migrations", neovateMigration)
	gooseMu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("unrecognized constraint silently migrated: %v", err)
	}
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, neovateMigration).Scan(&applied); err != nil || applied != 0 {
		t.Fatalf("failed migration marked applied: count = %d, err = %v", applied, err)
	}
}

// Pre-merge Neovate builds applied a retired 0209 migration, so their sessions
// constraint already allows Neovate. The timestamped migration must run over
// that schema, after every later main migration, without adding a duplicate.
func TestMigration20261114173957269SkipsPreMergeNeovateSchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeNeovateMigration)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''neovate'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (209, 1)`)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('nv', '/tmp/nv', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES ('nv-1', 'nv', 1, 'neovate', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate pre-merge Neovate profile: %v", err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if strings.Count(schema, "'neovate'") != 1 {
		t.Fatalf("pre-merge Neovate schema lost or duplicated Neovate: %s", schema)
	}
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, neovateMigration).Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("timestamped Neovate migration applied = %d, err = %v", applied, err)
	}
	var harness string
	if err := db.QueryRow(`SELECT harness FROM sessions WHERE id = 'nv-1'`).Scan(&harness); err != nil || harness != "neovate" {
		t.Fatalf("existing Neovate session = %q, %v", harness, err)
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
