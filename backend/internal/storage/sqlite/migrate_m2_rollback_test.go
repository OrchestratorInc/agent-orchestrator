package sqlite

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// m2LocalChain 是本地 M2 回滚测试的版本集合。
// 已合并 M1 迁移的缺陷仍由 TestM1DownPathLeavesChangeLogInvalid 记录，
// 正式登记继续由 shippedMigrations 维护；此处不复制未使用的 M1 列表。
var m2LocalChain = []struct {
	version int64
	name    string
}{
	{20261103143320688, "workflow_attempt_bindings"},
	{20261103160303854, "workflow_interventions"},
	{20261103163208174, "workflow_collaboration"},
	{20261103170009848, "workflow_message_loss_and_ref"},
}

// m2PreM2Version is the last migration before any workflow table exists.
const m2PreM2Version int64 = 20261102100313400

// m2FirstLocalVersion is the first M2 migration M2 owns.
const m2FirstLocalVersion int64 = 20261103143320688

// TestM1DownPathLeavesChangeLogInvalid documents a PRE-EXISTING defect in the committed M1
// migration chain. It is a limitation record, not a passing guarantee.
//
// The defect: 20261103073516664_workflow_cdc.sql's Down narrows change_log's event_type CHECK
// back to the pre-M1 list, but it does not delete the rows that carry the event types it is
// removing. Dropping a row's allowed type while the row survives makes the table violate its
// own CHECK. SQLite will not notice until something re-validates the schema, at which point:
//
//	PRAGMA integrity_check -> "CHECK constraint failed in change_log"
//
// The trigger rows themselves are dropped, and no new event is emitted, so the rollback is
// not noisy — it is just structurally invalid. A database rolled back below this version is
// unreadable for integrity purposes until the offending rows are deleted by hand.
//
// Why this is not fixed here: the file is part of the M1 acceptance record in a6c284a, and
// AGENTS.md forbids editing a merged migration, because a database that already applied
// version 20261103073516664 would never receive the change. The fix requires a new migration
// plus approval; it is recorded in docs/customization/m2-report.md section 7.
//
// This test therefore asserts the defect EXISTS, so it cannot be forgotten silently, and so
// that whoever fixes it sees this test fail and knows to update the report.
func TestM1DownPathLeavesChangeLogInvalid(t *testing.T) {
	db := openMigratedDatabaseCopy(t, m2LocalChain[len(m2LocalChain)-1].version)
	defer db.Close()

	seedM2RollbackActivity(t, db)
	if countWorkflowEvents(t, db) == 0 {
		t.Fatal("the seed produced no workflow event, so this test would prove nothing")
	}

	downTo(t, db, m2PreM2Version)

	// The defect: workflow events survive, and the table can no longer be integrity-checked.
	surviving := countWorkflowEvents(t, db)
	if surviving == 0 {
		t.Fatal("the committed Down now clears workflow events. The defect recorded in " +
			"m2-report.md section 7 appears FIXED — update this test and the report.")
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if integrity == "ok" {
		t.Fatalf("integrity_check is ok but %d workflow event(s) survived a downgrade. "+
			"Either the committed Down now clears them, or SQLite stopped validating — "+
			"re-derive the defect before trusting the report.", surviving)
	}
	if !strings.Contains(integrity, "change_log") {
		t.Fatalf("unexpected integrity failure %q; the recorded defect was in change_log", integrity)
	}
	t.Logf("CONFIRMED pre-existing defect: %d workflow event(s) survive the committed rollback "+
		"and integrity_check reports %q", surviving, integrity)
}

// TestM2LocalRollbacksEmitZeroEvents is the M2-owned half of the rollback sweep.
//
// It rolls back only the migrations M2 authored this cycle and proves:
//
//   - each one clears the change_log rows for the event types it introduced, so it cannot
//     leave events describing a table it just dropped;
//   - the pre-M2 session history is untouched (only M2's own events go);
//   - no M2 table and no M2 trigger survives;
//   - the database passes integrity_check afterwards, which is the assertion the committed
//     chain cannot make.
func TestM2LocalRollbacksEmitZeroEvents(t *testing.T) {
	db := openMigratedDatabaseCopy(t, m2LocalChain[len(m2LocalChain)-1].version)
	defer db.Close()

	seedM2RollbackActivity(t, db)

	eventsBefore := countAllEvents(t, db)
	ownedBefore := countEventsM2LocalOwns(t, db)
	if eventsBefore == 0 || ownedBefore == 0 {
		t.Fatal("the seed produced no events, so this test would prove nothing")
	}

	// Roll back exactly the M2-authored migrations, and no further.
	downTo(t, db, m2FirstLocalVersion-1)

	// (1) ZERO events of the types M2-local introduced survive: each local Down cleared its
	// own. The committed migration's own event types are deliberately NOT asserted here —
	// clearing those would be editing a merged migration's contract, and that defect is
	// recorded by the test above instead.
	if surviving := countEventsM2LocalOwns(t, db); surviving != 0 {
		t.Fatalf("%d event(s) of types M2-local introduced survived; each local Down must "+
			"delete the event types it introduced", surviving)
	}

	// (2) No event was ADDED, and only M2's own were removed.
	eventsAfter := countAllEvents(t, db)
	removed := eventsBefore - eventsAfter
	if removed < 0 {
		t.Fatalf("the rollback ADDED %d CDC event(s); a down migration must emit nothing",
			-eventsAfter+eventsBefore)
	}
	if removed != ownedBefore {
		t.Fatalf("the rollback removed %d event(s), want exactly the %d it owns",
			removed, ownedBefore)
	}

	// (3) Pre-M2 history is intact: the rollback must not delete what it does not own.
	if eventsAfter == 0 {
		t.Fatal("the rollback deleted every CDC event, including pre-M2 session history")
	}
	if !hasEventType(t, db, "session_created") {
		t.Fatal("the rollback deleted the pre-M2 session_created history")
	}

	// (4) No M2-LOCAL table survives. The workflow_core tables belong to the committed chain
	// and are out of scope here — see the ownership note at the top of this file.
	for _, table := range m2LocalTables {
		if tableExists(t, db, table) {
			t.Fatalf("table %q survived the M2-local rollback", table)
		}
	}

	// (5) No M2-LOCAL trigger survives. A surviving trigger is far worse than a surviving
	// table: it would fire on a future table of the same name and emit events for rows nothing
	// owns.
	for _, name := range listTriggers(t, db) {
		if isM2LocalTrigger(name) {
			t.Fatalf("trigger %q survived the M2-local rollback", name)
		}
	}

	// (6) The database is coherent after rolling back ONLY the M2-owned migrations.
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after the M2 rollback = %q, %v", integrity, err)
	}

	// (7) The chain rolls forward again, so the Down did not corrupt it.
	upTo(t, db, m2LocalChain[len(m2LocalChain)-1].version)
	if !tableExists(t, db, "workflow_messages") {
		t.Fatal("re-applying the M2 chain did not restore workflow_messages")
	}
}

// seedM2RollbackActivity writes one row into every M2 table that owns a CDC trigger, plus one
// pre-M2 session so the test can prove unrelated history survives.
func seedM2RollbackActivity(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)
	sha := strings.Repeat("a", 64)
	requestSHA := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('rb-proj', '/tmp/rb', ?);`, now)
	// A pre-M2 session: proves the rollback deletes only its OWN events.
	mustExec(t, db, `
INSERT INTO sessions
    (id, project_id, num, kind, activity_state, activity_last_at, is_terminated, created_at, updated_at)
VALUES ('rb-session', 'rb-proj', 1, 'worker', 'exited', ?, 1, ?, ?);`, now, now, now)

	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('rb-w', 'Rollback', 'Prove silence', 'active', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES ('rb-task', 'rb-w', 'development', 'T', 'running', 1, ?, ?);`, now, now)
	// A claimed attempt must carry a worker and a live lease: the CHECK forbids a claimed
	// row without them, so the fixture proves the constraint rather than bypassing it.
	mustExec(t, db, `
INSERT INTO workflow_attempts
    (id, workflow_id, task_id, attempt_no, worker_id, lease_token, lease_until,
     state, created_at, updated_at)
VALUES ('rb-attempt', 'rb-w', 'rb-task', 1, 'worker-1', ?, ?, 'claimed', ?, ?);`,
		sha, now.Add(time.Hour), now, now)
	mustExec(t, db, `
INSERT INTO workflow_messages
    (id, workflow_id, from_task_id, sender_kind, sender_task_id, kind, delivery_state,
     body, payload_sha, client_message_id, revision, created_at, updated_at)
VALUES ('rb-msg', 'rb-w', 'rb-task', 'agent', 'rb-task', 'progress', 'persisted',
        'body', ?, 'rb-client', 1, ?, ?);`, requestSHA, now, now)
}

// m2LocalEventTypes are the event types the M2-LOCAL migrations introduce, and therefore the
// only ones their Down sections may clear. Kept as an explicit list so a new local migration
// that forgets its cleanup fails the rollback test.
var m2LocalEventTypes = []string{
	"workflow_intervention_updated",
	"workflow_message_created",
	"workflow_message_updated",
	"workflow_task_request_updated",
}

// countEventsM2LocalOwns counts the change_log rows whose type a local M2 migration created.
func countEventsM2LocalOwns(t *testing.T, db *sql.DB) int {
	t.Helper()
	total := 0
	for _, eventType := range m2LocalEventTypes {
		var n int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM change_log WHERE event_type = ?`, eventType).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", eventType, err)
		}
		total += n
	}
	return total
}

// countWorkflowEvents counts the change_log rows carrying an M2 event type.
func countWorkflowEvents(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM change_log WHERE event_type LIKE 'workflow%'`).Scan(&n); err != nil {
		t.Fatalf("count workflow change_log: %v", err)
	}
	return n
}

// countAllEvents counts every change_log row.
func countAllEvents(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM change_log`).Scan(&n); err != nil {
		t.Fatalf("count change_log: %v", err)
	}
	return n
}

// hasEventType reports whether at least one row of this event type survives.
func hasEventType(t *testing.T, db *sql.DB, eventType string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM change_log WHERE event_type = ?`, eventType).Scan(&n); err != nil {
		t.Fatalf("check event type %q: %v", eventType, err)
	}
	return n > 0
}

// tableExists reports whether a table is present in the schema.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&n); err != nil {
		t.Fatalf("check table %q: %v", name, err)
	}
	return n > 0
}

// listTriggers returns every trigger name in the schema.
func listTriggers(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'trigger' ORDER BY name`)
	if err != nil {
		t.Fatalf("list triggers: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan trigger: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate triggers: %v", err)
	}
	return names
}

// m2LocalTables are the tables the M2-LOCAL migrations create, and therefore the ones their
// Down sections must drop.
var m2LocalTables = []string{
	"workflow_attempt_bindings",
	"workflow_attempt_repo_bindings",
	"workflow_interventions",
	"workflow_messages",
}

// isM2LocalTrigger reports whether a trigger was introduced by an M2-LOCAL migration. The
// workflow_cdc triggers belong to the committed chain and are excluded on purpose.
func isM2LocalTrigger(name string) bool {
	switch name {
	case "sessions_workflow_binding_loss", "sessions_workflow_intervention_loss",
		"sessions_workflow_message_loss":
		return true
	}
	switch name {
	case "workflow_attempts_cdc_insert", "workflow_attempts_cdc_update",
		"workflow_tasks_cdc_insert", "workflow_tasks_cdc_update",
		"workflows_cdc_insert", "workflows_cdc_update":
		// Owned by 20261103073516664_workflow_cdc.sql (committed).
		return false
	}
	return strings.HasPrefix(name, "workflow_attempt_bindings_") ||
		strings.HasPrefix(name, "workflow_attempt_repo_bindings_") ||
		strings.HasPrefix(name, "workflow_interventions_") ||
		strings.HasPrefix(name, "workflow_messages_")
}
