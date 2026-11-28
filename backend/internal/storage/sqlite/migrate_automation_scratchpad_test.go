package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// The migration lets an automation have no project (the Scratchpad) while a
// named project keeps its foreign key and cascade, and it rolls back only once
// no Scratchpad automation is left.
func TestAutomationScratchpadMigrationRoundTripFromCurrentMain(t *testing.T) {
	const baseline int64 = 20261119090000000
	const scratchpad int64 = 20261123090620327
	db := openMigratedDatabaseCopy(t, baseline)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='automations'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO automations(id,project_id,display_name,prompt,kind,harness,rrule_text,timezone,next_run_at,created_at,updated_at)
		VALUES (?,?,'Digest','Summarize','worker','codex','FREQ=DAILY','UTC',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`
	if _, err := db.Exec(insert, "before", nil); err == nil {
		t.Fatal("an automation without a project was stored before the migration")
	}

	upTo(t, db, scratchpad)
	mustExec(t, db, insert, "scratch", nil)
	mustExec(t, db, `INSERT INTO projects(id,path,registered_at) VALUES ('alpha','/tmp/alpha',CURRENT_TIMESTAMP)`)
	mustExec(t, db, insert, "alpha-1", "alpha")
	if _, err := db.Exec(insert, "orphan", "missing"); err == nil {
		t.Fatal("project foreign key no longer enforced")
	}
	mustExec(t, db, `DELETE FROM projects WHERE id='alpha'`)
	var remaining string
	if err := db.QueryRow(`SELECT group_concat(id) FROM automations`).Scan(&remaining); err != nil || remaining != "scratch" {
		t.Fatalf("automations after project delete = %q, %v; want only the Scratchpad one", remaining, err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity %s: %v", integrity, err)
	}

	gooseMu.Lock()
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	refused := goose.DownTo(db, "migrations", baseline)
	gooseMu.Unlock()
	if refused == nil {
		t.Fatal("rollback dropped a Scratchpad automation's missing project")
	}
	mustExec(t, db, `DELETE FROM automations WHERE project_id IS NULL`)
	downTo(t, db, baseline)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='automations'`).Scan(&after); err != nil || after != before {
		t.Fatalf("down migration changed prior schema: %v\n%s", err, after)
	}
	if !strings.Contains(after, "project_id    TEXT NOT NULL REFERENCES projects (id)") {
		t.Fatalf("restored schema lost NOT NULL:\n%s", after)
	}
}
