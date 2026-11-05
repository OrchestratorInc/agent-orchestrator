package sqlite

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

const (
	workflowInterventionsPreviousVersion int64 = 20261103143320688
	workflowInterventionsVersion         int64 = 20261103160303854
)

func TestMigrationWorkflowInterventionsSchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowInterventionsPreviousVersion)
	if err := migrate(db); err != nil {
		t.Fatalf("apply workflow interventions migration: %v", err)
	}

	wantColumns := []string{
		"id", "workflow_id", "task_id", "attempt_id", "mode", "state", "session_id",
		"controller_generation", "client_message_id", "operation_sha",
		"target_provider_turn_id", "chat_turn_id", "provider_turn_id", "reason_code",
		"queue_available", "revision", "created_at", "updated_at", "settled_at",
	}
	if got := tableColumns(t, db, "workflow_interventions"); !reflect.DeepEqual(got, wantColumns) {
		t.Fatalf("workflow_interventions columns = %v, want %v", got, wantColumns)
	}

	// The intervention table must not gain a message-body column: prompt text must never
	// reach the store or the SSE stream.
	for _, column := range wantColumns {
		lower := strings.ToLower(column)
		if lower == "text" || lower == "body" || strings.Contains(lower, "content") ||
			strings.Contains(lower, "prompt") || strings.Contains(lower, "message_text") {
			t.Fatalf("workflow_interventions gained a %q column; intervention text must never be stored here", column)
		}
	}

	for _, trigger := range []string{
		"sessions_workflow_intervention_loss",
		"workflow_interventions_cdc_insert",
		"workflow_interventions_cdc_update",
	} {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, trigger,
		).Scan(&count); err != nil {
			t.Fatalf("count trigger %s: %v", trigger, err)
		}
		if count != 1 {
			t.Fatalf("trigger %s is missing", trigger)
		}
	}
}

func TestMigrationWorkflowInterventionsDownRoundTrips(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowInterventionsVersion)
	if err := goose.DownTo(db, "migrations", workflowInterventionsPreviousVersion); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	for _, object := range []struct{ kind, name string }{
		{"table", "workflow_interventions"},
		{"trigger", "sessions_workflow_intervention_loss"},
		{"trigger", "workflow_interventions_cdc_insert"},
		{"trigger", "workflow_interventions_cdc_update"},
	} {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type=? AND name=?`, object.kind, object.name,
		).Scan(&count); err != nil {
			t.Fatalf("count %s %s: %v", object.kind, object.name, err)
		}
		if count != 0 {
			t.Fatalf("%s %s survived the down migration", object.kind, object.name)
		}
	}
}

// TestMigrationWorkflowInterventionsCDCWidening is the behavioural proof of the change_log
// widening: the new event type inserts, the event the trigger emits is readable, and an
// unknown type is still rejected by the CHECK.
func TestMigrationWorkflowInterventionsCDCWidening(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowInterventionsVersion)
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)

	// The widened CHECK accepts the new type.
	if _, err := db.Exec(`
INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
VALUES (NULL, NULL, 'workflow_intervention_updated', '{"kind":"workflow_intervention"}', ?);`, now); err != nil {
		t.Fatalf("the widened change_log rejected workflow_intervention_updated: %v", err)
	}

	// And it still rejects a type nobody widened for.
	if _, err := db.Exec(`
INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
VALUES (NULL, NULL, 'workflow_bogus_updated', '{}', ?);`, now); err == nil {
		t.Fatal("change_log accepted an unknown event type; the CHECK is no longer closed")
	}

	// Seed a workflow and task so an intervention can exist, then prove the trigger fires
	// on a state change only.
	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('iv-workflow', 'I', 'Interventions', 'active', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES ('iv-task', 'iv-workflow', 'development', 'Task', 'running', 1, ?, ?);`, now, now)

	mustExec(t, db, `
INSERT INTO workflow_interventions (
    id, workflow_id, task_id, attempt_id, mode, state, session_id,
    controller_generation, client_message_id, operation_sha, target_provider_turn_id,
    chat_turn_id, provider_turn_id, reason_code, queue_available, revision,
    created_at, updated_at, settled_at
) VALUES ('iv-1', 'iv-workflow', 'iv-task', 'iv-attempt', 'steer', 'requested', NULL,
    'gen-1', 'msg-1', ?, 'prov-1', '', '', '', 0, 1, ?, ?, NULL);`,
		strings.Repeat("a", 64), now, now)

	var baseline int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&baseline); err != nil {
		t.Fatalf("read baseline seq: %v", err)
	}

	// A state change emits exactly one event.
	mustExec(t, db, `
UPDATE workflow_interventions SET state = 'applied', chat_turn_id = 'turn-1',
    provider_turn_id = 'prov-1', settled_at = ?, updated_at = ?, revision = 2
WHERE id = 'iv-1';`, now, now)

	var payload string
	if err := db.QueryRow(`
SELECT payload FROM change_log
WHERE seq > ? AND event_type = 'workflow_intervention_updated'
ORDER BY seq DESC LIMIT 1`, baseline).Scan(&payload); err != nil {
		t.Fatalf("the state-change trigger emitted no event: %v", err)
	}
	for _, want := range []string{
		`"kind":"workflow_intervention"`, `"workflowId":"iv-workflow"`,
		`"interventionId":"iv-1"`, `"mode":"steer"`, `"state":"applied"`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload %s is missing %s", payload, want)
		}
	}
	// The payload must never carry message text or a client message id (which is a
	// caller-chosen value that could itself embed content).
	for _, forbidden := range []string{"msg-1", "clientMessageId", "text", "prompt"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("payload leaked %q: %s", forbidden, payload)
		}
	}

	// A non-state update emits nothing: no heartbeat flood.
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&baseline); err != nil {
		t.Fatalf("read baseline seq: %v", err)
	}
	mustExec(t, db, `
UPDATE workflow_interventions SET reason_code = 'note', updated_at = ? WHERE id = 'iv-1';`, now)
	var after int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&after); err != nil {
		t.Fatalf("read seq after non-state update: %v", err)
	}
	if after != baseline {
		t.Fatalf("a non-state update emitted %d events; only a state change may", after-baseline)
	}
}

// TestMigrationWorkflowInterventionsModeStateCheck proves the mode↔state CHECK at the SQL
// level, independent of the Go validation.
func TestMigrationWorkflowInterventionsModeStateCheck(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowInterventionsVersion)
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('ms-workflow', 'M', 'Mode state', 'active', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES ('ms-task', 'ms-workflow', 'development', 'Task', 'running', 1, ?, ?);`, now, now)

	insert := func(id, mode, state string) error {
		_, err := db.Exec(`
INSERT INTO workflow_interventions (
    id, workflow_id, task_id, attempt_id, mode, state, session_id,
    controller_generation, client_message_id, operation_sha, target_provider_turn_id,
    chat_turn_id, provider_turn_id, reason_code, queue_available, revision,
    created_at, updated_at, settled_at
) VALUES (?, 'ms-workflow', 'ms-task', 'ms-attempt', ?, ?, NULL,
    'gen-1', ?, ?, '', '', '', '', 0, 1, ?, ?, NULL);`, id, mode, state, "msg-"+id, strings.Repeat("b", 64), now, now)
		return err
	}

	if err := insert("ok-steer", "steer", "requested"); err != nil {
		t.Fatalf("legal steer row rejected: %v", err)
	}
	if err := insert("ok-stop", "stop", "stop_requested"); err != nil {
		t.Fatalf("legal stop row rejected: %v", err)
	}
	if err := insert("bad-steer-stop", "steer", "stop_confirmed"); err == nil {
		t.Fatal("a steer was accepted in a stop state")
	}
	if err := insert("bad-stop-applied", "stop", "applied"); err == nil {
		t.Fatal("a stop was accepted in applied")
	}
	if err := insert("bad-mode", "pause", "requested"); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

// TestMigrationWorkflowInterventionsSessionNullable proves session_id accepts NULL in every
// state: this is the M2-03 lesson applied, so a session delete can always land.
func TestMigrationWorkflowInterventionsSessionNullable(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowInterventionsVersion)
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('nn-workflow', 'N', 'Null session', 'active', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES ('nn-task', 'nn-workflow', 'development', 'Task', 'running', 1, ?, ?);`, now, now)

	for i, pair := range [][2]string{
		{"steer", "requested"}, {"steer", "queued"}, {"steer", "delivered"},
		{"steer", "applied"}, {"steer", "rejected"}, {"steer", "cancelled"},
		{"steer", "unknown"}, {"stop", "stop_requested"}, {"stop", "stop_confirmed"},
		{"stop", "stop_unknown"},
	} {
		if _, err := db.Exec(`
INSERT INTO workflow_interventions (
    id, workflow_id, task_id, attempt_id, mode, state, session_id,
    controller_generation, client_message_id, operation_sha, target_provider_turn_id,
    chat_turn_id, provider_turn_id, reason_code, queue_available, revision,
    created_at, updated_at, settled_at
) VALUES (?, 'nn-workflow', 'nn-task', 'nn-attempt', ?, ?, NULL,
    'gen-1', ?, ?, '', '', '', '', 0, 1, ?, ?, NULL);`,
			"nn-"+string(rune('a'+i)), pair[0], pair[1], "msg-nn-"+string(rune('a'+i)),
			strings.Repeat("c", 64), now, now); err != nil {
			t.Fatalf("state %q must accept a NULL session: %v", pair[1], err)
		}
	}
}

// TestMigrationWorkflowInterventionsRequiresGeneration proves controller_generation cannot be
// blank: generation fencing is worthless if the fence value is optional.
func TestMigrationWorkflowInterventionsRequiresGeneration(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowInterventionsVersion)
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('g-workflow', 'G', 'Generation', 'active', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES ('g-task', 'g-workflow', 'development', 'Task', 'running', 1, ?, ?);`, now, now)

	if _, err := db.Exec(`
INSERT INTO workflow_interventions (
    id, workflow_id, task_id, attempt_id, mode, state, session_id,
    controller_generation, client_message_id, operation_sha, target_provider_turn_id,
    chat_turn_id, provider_turn_id, reason_code, queue_available, revision,
    created_at, updated_at, settled_at
) VALUES ('g-1', 'g-workflow', 'g-task', NULL, 'queue', 'requested', NULL,
    '  ', 'msg-g', ?, '', '', '', '', 0, 1, ?, ?, NULL);`,
		strings.Repeat("d", 64), now, now); err == nil {
		t.Fatal("a blank controller_generation was accepted")
	}
	// A short operation_sha must also be rejected: the fingerprint rule needs 64 chars.
	if _, err := db.Exec(`
INSERT INTO workflow_interventions (
    id, workflow_id, task_id, attempt_id, mode, state, session_id,
    controller_generation, client_message_id, operation_sha, target_provider_turn_id,
    chat_turn_id, provider_turn_id, reason_code, queue_available, revision,
    created_at, updated_at, settled_at
) VALUES ('g-2', 'g-workflow', 'g-task', NULL, 'queue', 'requested', NULL,
    'gen-1', 'msg-g2', 'short', '', '', '', '', 0, 1, ?, ?, NULL);`, now, now); err == nil {
		t.Fatal("a short operation_sha was accepted")
	}
}
