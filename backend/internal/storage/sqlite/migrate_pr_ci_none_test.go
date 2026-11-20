package sqlite

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	prCINoneMigration     = int64(20261022051758732)
	prCINoneBaseMigration = int64(20261021230230412)
)

// seedPRCISession inserts the project and session a pr row hangs off.
func seedPRCISession(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('ci-project', '/tmp/ci-project', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES ('ci-session', 'ci-project', 1, 'claude-code', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
}

const insertPRWithCI = `INSERT INTO pr (url, session_id, number, ci_state, updated_at) VALUES (?, 'ci-session', ?, ?, CURRENT_TIMESTAMP)`

func prSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'pr'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

// Break caught: a PR whose commit has no checks was stored as 'unknown', so the
// panel waited on checks that never arrive (#7044). The state needs its own
// value, and the CHECK constraint has to accept it without rebuilding the table.
func TestMigration20261022051758732AllowsNoneCIStateAndReverses(t *testing.T) {
	db := openMigratedDatabaseCopy(t, prCINoneBaseMigration)
	upTo(t, db, prCINoneBaseMigration)
	seedPRCISession(t, db)
	before := prSchema(t, db)
	mustExec(t, db, insertPRWithCI, "https://github.com/o/r/pull/1", 1, "passing")
	if _, err := db.Exec(insertPRWithCI, "https://github.com/o/r/pull/2", 2, "none"); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("none before the migration = %v; want CHECK failure", err)
	}

	upTo(t, db, prCINoneMigration)
	if _, err := db.Exec(insertPRWithCI, "https://github.com/o/r/pull/2", 2, "none"); err != nil {
		t.Fatalf("insert none after migration: %v", err)
	}
	if _, err := db.Exec(insertPRWithCI, "https://github.com/o/r/pull/3", 3, "bogus"); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("unknown CI state bypassed the CHECK constraint: %v", err)
	}
	var passing int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pr WHERE ci_state = 'passing'`).Scan(&passing); err != nil || passing != 1 {
		t.Fatalf("existing passing rows = %d, err = %v; want 1", passing, err)
	}
	// Only the ci_state clause may change; the other CHECKs (review_decision
	// also has a 'none') and every column stay as they were.
	after := prSchema(t, db)
	want := strings.Replace(before, `CHECK (ci_state IN ('unknown', 'pending', 'passing', 'failing'))`,
		`CHECK (ci_state IN ('unknown', 'none', 'pending', 'passing', 'failing'))`, 1)
	if after != want {
		t.Fatalf("pr schema after migration differs from the expected rewrite:\n%s", after)
	}

	gooseMu.Lock()
	rollbackErr := goose.DownTo(db, "migrations", prCINoneBaseMigration)
	gooseMu.Unlock()
	if rollbackErr == nil {
		t.Fatal("rollback accepted an existing none row")
	}
	mustExec(t, db, `UPDATE pr SET ci_state = 'unknown' WHERE ci_state = 'none'`)
	downTo(t, db, prCINoneBaseMigration)
	if restored := prSchema(t, db); restored != before {
		t.Fatalf("down migration did not restore the original schema:\n%s", restored)
	}
	if _, err := db.Exec(insertPRWithCI, "https://github.com/o/r/pull/4", 4, "none"); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("none after downgrade = %v; want CHECK failure", err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
	}
	var fkViolation int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&fkViolation); err != nil || fkViolation != 0 {
		t.Fatalf("foreign key violations after downgrade = %d, %v", fkViolation, err)
	}
}

// Break caught: the schema rewrite silently did nothing on a pr table whose
// constraint has a shape it does not recognize, leaving 'none' rejected.
func TestMigration20261022051758732RejectsUnrecognizedConstraint(t *testing.T) {
	db := openMigratedDatabaseCopy(t, prCINoneBaseMigration)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''failing''))', '''failing'', ''other''))') WHERE type = 'table' AND name = 'pr' AND sql LIKE '%ci_state IN%'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	gooseMu.Lock()
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		gooseMu.Unlock()
		t.Fatal(err)
	}
	err := goose.UpTo(db, "migrations", prCINoneMigration)
	gooseMu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("unrecognized constraint silently migrated: %v", err)
	}
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, prCINoneMigration).Scan(&applied); err != nil || applied != 0 {
		t.Fatalf("failed migration marked applied: count = %d, err = %v", applied, err)
	}
}
