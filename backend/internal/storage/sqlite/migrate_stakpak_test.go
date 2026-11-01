package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	stakpakMigration        = 20261101191148503
	stakpakPriorMigration   = 20261101055144579
	stakpakInsertSessionSQL = `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'stk-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
)

// TestMigrationAllowStakpakHarness checks that the new harness is insertable,
// an unknown harness is still rejected, a rollback with Stakpak rows is
// refused, and the down migration restores the exact prior constraint.
func TestMigrationAllowStakpakHarness(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 215)
	upTo(t, db, stakpakPriorMigration)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('stk-project', '/tmp/stk-project', CURRENT_TIMESTAMP)`)
	mustExec(t, db, stakpakInsertSessionSQL, "existing-hermes", 1, "hermes")
	if _, err := db.Exec(stakpakInsertSessionSQL, "too-early", 2, "stakpak"); err == nil {
		t.Fatal("stakpak accepted before its migration")
	}

	upTo(t, db, stakpakMigration)
	if _, err := db.Exec(stakpakInsertSessionSQL, "stk-session", 3, "stakpak"); err != nil {
		t.Fatalf("insert stakpak session after migration: %v", err)
	}
	if _, err := db.Exec(stakpakInsertSessionSQL, "unknown", 4, "unknown-agent"); err == nil {
		t.Fatal("unknown harness bypassed the CHECK constraint")
	}

	gooseMu.Lock()
	rollbackErr := goose.DownTo(db, "migrations", stakpakPriorMigration)
	gooseMu.Unlock()
	if rollbackErr == nil {
		t.Fatal("rollback accepted an existing stakpak session")
	}
	mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'stakpak'`)
	downTo(t, db, stakpakPriorMigration)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("down migration did not restore original schema: %v", err)
	}
	if _, err := db.Exec(stakpakInsertSessionSQL, "after-down", 5, "stakpak"); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("stakpak insertion after downgrade = %v; want CHECK failure", err)
	}
}
