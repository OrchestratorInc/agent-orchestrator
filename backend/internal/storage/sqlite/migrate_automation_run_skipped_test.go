package sqlite

import (
	"strings"
	"testing"
)

const automationRunSkippedMigrationVersion int64 = 20261127080000000

// The migration widens the automation_runs status CHECK to accept 'skipped'
// and keeps rejecting unknown statuses. Down restores the exact prior schema
// and records skipped rows as the failed rows earlier builds wrote.
func TestAutomationRunSkippedStatusMigrationRoundTrip(t *testing.T) {
	const baseline int64 = 20261126154409684
	db := openMigratedDatabaseCopy(t, baseline)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='automation_runs'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, "'skipped'") {
		t.Fatal("skipped status present before its migration")
	}
	mustExec(t, db, `INSERT INTO projects(id,path,registered_at) VALUES ('skipped-project','/tmp/skipped-project',CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO automations(id,project_id,display_name,prompt,kind,rrule_text,timezone,next_run_at,created_at,updated_at)
		VALUES ('skipped-automation','skipped-project','Hourly','Review','worker','FREQ=HOURLY','UTC',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO automation_runs(id,automation_id,scheduled_for,status,created_at,updated_at)
		VALUES (?,'skipped-automation',?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`
	if _, err := db.Exec(insert, "before-up", "2026-08-25 09:00:00", "skipped"); err == nil {
		t.Fatal("skipped status accepted before its migration")
	}

	upTo(t, db, automationRunSkippedMigrationVersion)
	mustExec(t, db, insert, "skipped-run", "2026-08-25 10:00:00", "skipped")
	mustExec(t, db, insert, "failed-run", "2026-08-25 11:00:00", "failed")
	if _, err := db.Exec(insert, "unknown-run", "2026-08-25 12:00:00", "bogus"); err == nil {
		t.Fatal("unknown status bypassed CHECK")
	}

	downTo(t, db, baseline)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='automation_runs'`).Scan(&after); err != nil || after != before {
		t.Fatalf("down migration changed prior schema: %v\nbefore: %s\nafter:  %s", err, before, after)
	}
	var status, message string
	if err := db.QueryRow(`SELECT status, COALESCE(error_message, '') FROM automation_runs WHERE id='skipped-run'`).Scan(&status, &message); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !strings.HasPrefix(message, "Skipped:") {
		t.Fatalf("skipped row after down = %q %q, want failed with a skip message", status, message)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity %s: %v", integrity, err)
	}

	upTo(t, db, automationRunSkippedMigrationVersion)
	mustExec(t, db, insert, "skipped-again", "2026-08-25 13:00:00", "skipped")
}
