package sqlite

import (
	"strings"
	"testing"
	"time"
)

const (
	workflowCDCVersion         int64 = 20261103073516664
	workflowCDCPreviousVersion int64 = 20261103050931629
)

var workflowCDCTriggers = []string{
	"workflows_cdc_insert",
	"workflows_cdc_update",
	"workflow_tasks_cdc_insert",
	"workflow_tasks_cdc_update",
	"workflow_attempts_cdc_insert",
	"workflow_attempts_cdc_update",
}

var workflowCDCEventTypes = []string{
	"workflow_created",
	"workflow_updated",
	"workflow_task_updated",
	"workflow_attempt_updated",
}

// TestMigrationWorkflowCDCWidensChangeLogAndInstallsTriggers covers the whole
// migration in one pass: the CHECK is widened in place without losing the
// existing change_log triggers, every workflow trigger exists, the widened
// values are accepted, an unsupported value is still rejected, and each trigger
// emits exactly the intended event.
func TestMigrationWorkflowCDCWidensChangeLogAndInstallsTriggers(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCDCPreviousVersion)

	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'change_log'`).Scan(&before); err != nil {
		t.Fatalf("read change_log schema before migration: %v", err)
	}
	for _, eventType := range workflowCDCEventTypes {
		if strings.Contains(before, "'"+eventType+"'") {
			t.Fatalf("change_log already accepts %q before the migration: %s", eventType, before)
		}
	}
	if _, err := db.Exec(`INSERT INTO change_log (event_type, payload) VALUES ('workflow_created', '{}')`); err == nil {
		t.Fatal("narrowed change_log accepted workflow_created before the migration")
	}

	// The widening must not disturb the change_log CHECK's neighbours or the
	// dozens of triggers the table already carries.
	var triggersBefore int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger'`).Scan(&triggersBefore); err != nil {
		t.Fatalf("count triggers before migration: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("apply workflow cdc migration: %v", err)
	}

	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'change_log'`).Scan(&after); err != nil {
		t.Fatalf("read change_log schema after migration: %v", err)
	}
	for _, eventType := range workflowCDCEventTypes {
		if !strings.Contains(after, "'"+eventType+"'") {
			t.Fatalf("change_log does not accept %q after the migration: %s", eventType, after)
		}
	}
	// The pre-existing entries keep their order and comma layout; the new ones
	// are inserted directly after 'review_run_created'.
	if !strings.Contains(after, "'review_run_updated'") ||
		strings.Index(after, "'review_run_created'") > strings.Index(after, "'workflow_created'") ||
		strings.Index(after, "'workflow_attempt_updated'") > strings.Index(after, "'review_run_updated'") {
		t.Fatalf("widened event_type list is out of order: %s", after)
	}

	for _, trigger := range workflowCDCTriggers {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&count); err != nil {
			t.Fatalf("inspect trigger %s: %v", trigger, err)
		}
		if count != 1 {
			t.Errorf("trigger %s count = %d, want 1", trigger, count)
		}
	}
	var triggersAfter int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger'`).Scan(&triggersAfter); err != nil {
		t.Fatalf("count triggers after migration: %v", err)
	}
	if triggersAfter != triggersBefore+len(workflowCDCTriggers) {
		t.Errorf("trigger count = %d, want %d: the in-place widening dropped or added triggers",
			triggersAfter, triggersBefore+len(workflowCDCTriggers))
	}
	// The widening is schema text only: the nullable project_id from 0140 and
	// the retention index must both survive.
	for _, check := range []struct {
		query string
		want  int
	}{
		{`SELECT "notnull" FROM pragma_table_info('change_log') WHERE name = 'project_id'`, 0},
		{`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_change_log_created_at_seq'`, 1},
		{`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_change_log_project'`, 1},
	} {
		var got int
		if err := db.QueryRow(check.query).Scan(&got); err != nil {
			t.Fatalf("postcondition %q: %v", check.query, err)
		}
		if got != check.want {
			t.Errorf("postcondition %q = %d, want %d", check.query, got, check.want)
		}
	}

	// The widened CHECK accepts every new event type directly.
	for _, eventType := range workflowCDCEventTypes {
		mustExec(t, db, `INSERT INTO change_log (event_type, payload) VALUES (?, '{}')`, eventType)
	}
	if _, err := db.Exec(`INSERT INTO change_log (event_type, payload) VALUES ('workflow_archived', '{}')`); err == nil {
		t.Fatal("widened change_log accepted an event type that is not in the CHECK")
	}
}

// TestMigrationWorkflowCDCTriggersEmitScopedEvents asserts the payload shape,
// the null project/session scope, and the UPDATE guards for all six triggers.
func TestMigrationWorkflowCDCEmitsTriggers(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCDCPreviousVersion)
	if err := migrate(db); err != nil {
		t.Fatalf("apply workflow cdc migration: %v", err)
	}

	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
		VALUES ('workflow-cdc', 'CDC', 'Verify triggers', 'draft', 1, ?, ?)`, now, now)
	mustExec(t, db, `INSERT INTO workflow_tasks (
			id, workflow_id, kind, title, state, max_attempts, revision, created_at, updated_at)
		VALUES ('task-cdc', 'workflow-cdc', 'development', 'CDC task', 'pending', 2, 1, ?, ?)`, now, now)
	mustExec(t, db, `INSERT INTO workflow_attempts (
			id, workflow_id, task_id, attempt_no, worker_id, lease_token, lease_until,
			state, created_at, updated_at)
		VALUES ('attempt-cdc', 'workflow-cdc', 'task-cdc', 1, 'worker-cdc', 'lease-cdc', ?, 'claimed', ?, ?)`,
		now.Add(time.Minute), now, now)

	// State changes on each table emit one more event.
	mustExec(t, db, `UPDATE workflows SET state = 'active', revision = 2, updated_at = ? WHERE id = 'workflow-cdc'`, now)
	mustExec(t, db, `UPDATE workflow_tasks SET state = 'ready', revision = 2, updated_at = ? WHERE id = 'task-cdc'`, now)
	mustExec(t, db, `UPDATE workflow_attempts SET state = 'running', updated_at = ? WHERE id = 'attempt-cdc'`, now)

	// No-op updates that change neither state nor revision must stay silent, and
	// the attempt trigger is guarded on state alone so heartbeat/lease rewrites
	// cannot flood the log.
	mustExec(t, db, `UPDATE workflow_tasks SET priority = 5 WHERE id = 'task-cdc'`)
	mustExec(t, db, `UPDATE workflows SET title = 'CDC renamed' WHERE id = 'workflow-cdc'`)
	mustExec(t, db, `UPDATE workflow_attempts SET heartbeat_at = ?, updated_at = ? WHERE id = 'attempt-cdc'`, now, now)

	rows, err := db.Query(`SELECT event_type, project_id, session_id, payload
		FROM change_log
		WHERE event_type LIKE 'workflow_%'
		ORDER BY seq`)
	if err != nil {
		t.Fatalf("read workflow change_log rows: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var got []workflowEventRow
	for rows.Next() {
		var eventType string
		var projectID, sessionID *string
		var payload string
		if err := rows.Scan(&eventType, &projectID, &sessionID, &payload); err != nil {
			t.Fatalf("scan workflow change_log row: %v", err)
		}
		if projectID != nil || sessionID != nil {
			t.Errorf("%s row carried project_id=%v session_id=%v, want both NULL",
				eventType, projectID, sessionID)
		}
		got = append(got, workflowEventRow{eventType: eventType, payload: payload})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate workflow change_log rows: %v", err)
	}

	want := []string{
		"workflow_created",
		"workflow_task_updated",
		"workflow_attempt_updated",
		"workflow_updated",
		"workflow_task_updated",
		"workflow_attempt_updated",
	}
	if len(got) != len(want) {
		t.Fatalf("workflow events = %d (%v), want %d", len(got), workflowEventTypeNames(got), len(want))
	}
	for i, eventType := range want {
		if got[i].eventType != eventType {
			t.Errorf("event %d = %q, want %q (all=%v)", i, got[i].eventType, eventType, workflowEventTypeNames(got))
		}
	}

	for _, snippet := range []string{
		`"kind":"workflow"`, `"workflowId":"workflow-cdc"`, `"revision":2`, `"state":"active"`,
	} {
		if !strings.Contains(got[3].payload, snippet) {
			t.Errorf("workflow_updated payload %s is missing %s", got[3].payload, snippet)
		}
	}
	for _, snippet := range []string{
		`"kind":"workflow_task"`, `"workflowId":"workflow-cdc"`, `"taskId":"task-cdc"`, `"state":"ready"`,
	} {
		if !strings.Contains(got[4].payload, snippet) {
			t.Errorf("workflow_task_updated payload %s is missing %s", got[4].payload, snippet)
		}
	}
	for _, snippet := range []string{
		`"kind":"workflow_attempt"`, `"attemptId":"attempt-cdc"`, `"attemptNo":1`, `"state":"running"`,
	} {
		if !strings.Contains(got[5].payload, snippet) {
			t.Errorf("workflow_attempt_updated payload %s is missing %s", got[5].payload, snippet)
		}
	}
	// The lease token is a capability: it must never reach change_log, which is
	// replayed to every SSE subscriber.
	for _, event := range got {
		if strings.Contains(event.payload, "lease-cdc") || strings.Contains(event.payload, "worker-cdc") {
			t.Errorf("%s payload leaked the lease token or worker id: %s", event.eventType, event.payload)
		}
	}
}

type workflowEventRow struct {
	eventType string
	payload   string
}

func workflowEventTypeNames(events []workflowEventRow) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.eventType)
	}
	return names
}

// TestMigrationWorkflowCDCDownRestoresChangeLogAndDropsTriggers pins the rollback
// contract: the four event types are rejected again and the triggers are gone.
func TestMigrationWorkflowCDCDownRestoresChangeLogAndDropsTriggers(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCDCVersion)

	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'change_log'`).Scan(&before); err != nil {
		t.Fatalf("read widened change_log schema: %v", err)
	}
	var triggersBefore int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger'`).Scan(&triggersBefore); err != nil {
		t.Fatalf("count triggers before rollback: %v", err)
	}

	downTo(t, db, workflowCDCPreviousVersion)

	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'change_log'`).Scan(&after); err != nil {
		t.Fatalf("read change_log schema after rollback: %v", err)
	}
	for _, eventType := range workflowCDCEventTypes {
		if strings.Contains(after, "'"+eventType+"'") {
			t.Fatalf("change_log still accepts %q after rollback: %s", eventType, after)
		}
	}
	if !strings.Contains(after, "'review_run_updated'") {
		t.Fatalf("rollback removed a pre-existing event type: %s", after)
	}
	for _, trigger := range workflowCDCTriggers {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&count); err != nil {
			t.Fatalf("inspect trigger %s after rollback: %v", trigger, err)
		}
		if count != 0 {
			t.Errorf("trigger %s remains after rollback", trigger)
		}
	}
	var triggersAfter int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger'`).Scan(&triggersAfter); err != nil {
		t.Fatalf("count triggers after rollback: %v", err)
	}
	if triggersAfter != triggersBefore-len(workflowCDCTriggers) {
		t.Errorf("trigger count after rollback = %d, want %d: rollback dropped unrelated triggers",
			triggersAfter, triggersBefore-len(workflowCDCTriggers))
	}
	for _, eventType := range workflowCDCEventTypes {
		if _, err := db.Exec(`INSERT INTO change_log (event_type, payload) VALUES (?, '{}')`, eventType); err == nil ||
			!strings.Contains(strings.ToUpper(err.Error()), "CHECK") {
			t.Fatalf("%s after rollback = %v; want a CHECK failure", eventType, err)
		}
	}

	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after rollback = %q, %v; want ok", integrity, err)
	}
}

// TestMigrationWorkflowCDCGuardRejectsNoOpWidening pins the reason the
// migration ends in a content guard instead of trusting the UPDATE's row count:
// replace() with an anchor that does not match still reports one changed row and
// commits a schema that never widened. The guard must therefore fail on a
// database whose change_log text was not actually rewritten.
func TestMigrationWorkflowCDCGuardRejectsNoOpWidening(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCDCPreviousVersion)

	mustExec(t, db, `PRAGMA writable_schema = ON`)
	result, err := db.Exec(`UPDATE sqlite_master
SET sql = replace(
    sql,
    '            ''review_run_created_typo'',',
    '            ''review_run_created'',
            ''workflow_created'',
            ''workflow_updated'',
            ''workflow_task_updated'',
            ''workflow_attempt_updated'','
)
WHERE type = 'table' AND name = 'change_log'`)
	if err != nil {
		t.Fatalf("no-op writable_schema update: %v", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("rows affected: %v", err)
	}
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	if changed != 1 {
		t.Fatalf("no-op update reported %d changed rows, want 1: the guard exists because SQLite reports this as a success", changed)
	}

	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'change_log'`).Scan(&schema); err != nil {
		t.Fatalf("read change_log schema: %v", err)
	}
	if strings.Contains(schema, "'workflow_created'") {
		t.Fatal("the no-op update widened change_log after all; the guard premise is stale")
	}

	guard := `CREATE TEMP TABLE workflow_cdc_guard_probe (widened INTEGER CHECK (widened = 4));
INSERT INTO workflow_cdc_guard_probe
SELECT
    (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_created''') > 0)
  + (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_updated''') > 0)
  + (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_task_updated''') > 0)
  + (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_attempt_updated''') > 0);`
	if _, err := db.Exec(guard); err == nil || !strings.Contains(strings.ToUpper(err.Error()), "CHECK") {
		t.Fatalf("guard accepted an unwidened change_log: %v", err)
	}
	mustExec(t, db, `DROP TABLE IF EXISTS workflow_cdc_guard_probe`)

	// The real migration, on the same database, widens and passes the same guard.
	if err := migrate(db); err != nil {
		t.Fatalf("apply workflow cdc migration after the failed probe: %v", err)
	}
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'change_log'`).Scan(&schema); err != nil {
		t.Fatalf("read widened change_log schema: %v", err)
	}
	for _, eventType := range workflowCDCEventTypes {
		if !strings.Contains(schema, "'"+eventType+"'") {
			t.Fatalf("migration did not widen %q: %s", eventType, schema)
		}
	}
}
