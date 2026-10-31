package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const qoderCLIMigrationVersion int64 = 20261031120000000

// TestMigrationQoderCLIAllowsAndReversesTheHarness mirrors the hermes coverage:
// the new harness must be insertable, an unknown harness must still be rejected,
// a rollback that would strand existing rows must be refused, and a clean
// rollback must restore the exact prior constraint.
func TestMigrationQoderCLIAllowsAndReversesTheHarness(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 215)
	upTo(t, db, 20261019142324385)

	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('qoder-project', '/tmp/qoder-project', CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'qoder-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	mustExec(t, db, insert, "existing-omp", 1, "omp")

	upTo(t, db, qoderCLIMigrationVersion)
	if _, err := db.Exec(insert, "qoder-session", 2, "qodercli"); err != nil {
		t.Fatalf("insert qodercli session after migration: %v", err)
	}
	if _, err := db.Exec(insert, "unknown", 3, "unknown-agent"); err == nil {
		t.Fatal("unknown harness bypassed the CHECK constraint")
	}

	gooseMu.Lock()
	err := goose.DownTo(db, "migrations", 20261019142324385)
	gooseMu.Unlock()
	if err == nil {
		t.Fatal("rollback accepted an existing qodercli session")
	}

	mustExec(t, db, `UPDATE sessions SET harness = 'omp' WHERE harness = 'qodercli'`)
	downTo(t, db, 20261019142324385)

	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("down migration did not restore the prior constraint:\nbefore %s\nafter  %s", before, after)
	}
	if _, err := db.Exec(insert, "qoder-after-down", 4, "qodercli"); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("qodercli insertion after downgrade = %v; want CHECK failure", err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
	}
}
