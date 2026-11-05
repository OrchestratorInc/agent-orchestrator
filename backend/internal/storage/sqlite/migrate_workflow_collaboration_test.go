package sqlite

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

const (
	workflowCollaborationPreviousVersion int64 = 20261103160303854
	workflowCollaborationVersion         int64 = 20261103163208174
)

func TestMigrationWorkflowCollaborationSchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCollaborationPreviousVersion)
	if err := migrate(db); err != nil {
		t.Fatalf("apply workflow collaboration migration: %v", err)
	}

	wantMessages := []string{
		"id", "workflow_id", "from_task_id", "to_task_id", "sender_session_id",
		"target_session_id", "sender_kind", "sender_task_id", "kind", "delivery_state",
		"body", "payload_ref", "client_message_id", "payload_sha", "revision",
		"created_at", "updated_at",
	}
	if got := tableColumns(t, db, "workflow_messages"); !reflect.DeepEqual(got, wantMessages) {
		t.Fatalf("workflow_messages columns = %v, want %v", got, wantMessages)
	}

	wantRequests := []string{
		"id", "workflow_id", "requester_task_id", "request_kind", "request_json",
		"request_sha", "state", "decided_by_kind", "decided_by_ref", "decided_at",
		"reason", "workflow_revision", "revision", "created_at", "updated_at",
	}
	if got := tableColumns(t, db, "workflow_task_requests"); !reflect.DeepEqual(got, wantRequests) {
		t.Fatalf("workflow_task_requests columns = %v, want %v", got, wantRequests)
	}

	// Correction 5: the decider is decomposed, not one opaque column.
	for _, column := range tableColumns(t, db, "workflow_task_requests") {
		if column == "approver" {
			t.Fatal("workflow_task_requests kept a single opaque approver column")
		}
	}

	for _, trigger := range []string{
		"sessions_workflow_message_loss",
		"workflow_messages_cdc_insert",
		"workflow_messages_cdc_update",
		"workflow_task_requests_cdc_insert",
		"workflow_task_requests_cdc_update",
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

	// The request table must not gain a column that could carry a materialized plan.
	for _, column := range tableColumns(t, db, "workflow_task_requests") {
		if column == "created_task_id" || column == "materialized" || column == "applied_at" {
			t.Fatalf("workflow_task_requests gained %q, which would imply materialization", column)
		}
	}
}

func TestMigrationWorkflowCollaborationDownRoundTrips(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCollaborationVersion)
	if err := goose.DownTo(db, "migrations", workflowCollaborationPreviousVersion); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	for _, object := range []struct{ kind, name string }{
		{"table", "workflow_messages"},
		{"table", "workflow_task_requests"},
		{"trigger", "sessions_workflow_message_loss"},
		{"trigger", "workflow_messages_cdc_insert"},
		{"trigger", "workflow_task_requests_cdc_insert"},
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

// TestMigrationWorkflowCollaborationCDCWidening is the behavioural proof of the 14 → 17
// widening: all three new types insert, a bogus type still fails, and the pre-existing types
// still insert (so replace() did not truncate the list).
func TestMigrationWorkflowCollaborationCDCWidening(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCollaborationVersion)
	now := time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)

	insert := func(eventType string) error {
		_, err := db.Exec(`
INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
VALUES (NULL, NULL, ?, '{}', ?);`, eventType, now)
		return err
	}

	for _, eventType := range []string{
		"workflow_message_created",
		"workflow_message_updated",
		"workflow_task_request_updated",
	} {
		if err := insert(eventType); err != nil {
			t.Fatalf("the widened change_log rejected %s: %v", eventType, err)
		}
	}
	// Pre-existing types still insert: the widening must not have replaced the list.
	for _, eventType := range []string{
		"session_created", "session_updated", "workflow_created",
		"workflow_attempt_updated", "workflow_intervention_updated",
	} {
		if err := insert(eventType); err != nil {
			t.Fatalf("the widening truncated the list; %s no longer inserts: %v", eventType, err)
		}
	}
	// And an unknown type is still rejected.
	if err := insert("workflow_bogus_created"); err == nil {
		t.Fatal("change_log accepted an unknown event type; the CHECK is no longer closed")
	}
}

// TestMigrationWorkflowCollaborationCDCPayloadHasNoBody proves the payload carries ids only.
func TestMigrationWorkflowCollaborationCDCPayloadHasNoBody(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCollaborationVersion)
	now := time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)

	mustExec(t, db, `
INSERT INTO projects (id, path, registered_at) VALUES ('cv-proj', '/tmp/cv-proj', ?);`, now)
	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('cv-workflow', 'C', 'Collab', 'active', 2, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES ('cv-task-a', 'cv-workflow', 'development', 'A', 'running', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES ('cv-task-b', 'cv-workflow', 'testing', 'B', 'ready', 1, ?, ?);`, now, now)

	var baseline int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&baseline); err != nil {
		t.Fatalf("read baseline seq: %v", err)
	}

	const secretBody = "the api key is sk-live-SHOULD-NOT-LEAK"
	mustExec(t, db, `
INSERT INTO workflow_messages (
    id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
) VALUES ('cv-msg', 'cv-workflow', 'cv-task-a', 'cv-task-b', NULL, NULL,
    'agent', 'cv-task-a', 'question', 'persisted', ?, 'artifact-SECRETREF',
    'cv-client-msg', ?, 1, ?, ?);`, secretBody, strings.Repeat("a", 64), now, now)

	var payload string
	if err := db.QueryRow(`
SELECT payload FROM change_log
WHERE seq > ? AND event_type = 'workflow_message_created'
ORDER BY seq DESC LIMIT 1`, baseline).Scan(&payload); err != nil {
		t.Fatalf("the insert trigger emitted no event: %v", err)
	}
	for _, want := range []string{
		`"kind":"workflow_message"`, `"workflowId":"cv-workflow"`, `"messageId":"cv-msg"`,
		`"fromTaskId":"cv-task-a"`, `"toTaskId":"cv-task-b"`, `"messageKind":"question"`,
		`"senderKind":"agent"`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload %s is missing %s", payload, want)
		}
	}
	// The payload must never carry the body, the payload reference, or the client message id.
	for _, forbidden := range []string{"sk-live", "SHOULD-NOT-LEAK", "artifact-SECRETREF", "cv-client-msg", "body"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("payload leaked %q: %s", forbidden, payload)
		}
	}

	// The task-request trigger has the same property for request_json and reason.
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&baseline); err != nil {
		t.Fatalf("read baseline seq: %v", err)
	}
	mustExec(t, db, `
INSERT INTO workflow_task_requests (
    id, workflow_id, requester_task_id, request_kind, request_json, request_sha,
    state, decided_by_kind, decided_by_ref, decided_at, reason, workflow_revision,
    revision, created_at, updated_at
) VALUES ('cv-req', 'cv-workflow', 'cv-task-a', 'create_task', '{"note":"SECRETJSON"}',
    ?, 'pending', NULL, '', NULL, '', 2, 1, ?, ?);`, strings.Repeat("b", 64), now, now)

	if err := db.QueryRow(`
SELECT payload FROM change_log
WHERE seq > ? AND event_type = 'workflow_task_request_updated'
ORDER BY seq DESC LIMIT 1`, baseline).Scan(&payload); err != nil {
		t.Fatalf("the request insert trigger emitted no event: %v", err)
	}
	for _, forbidden := range []string{"SECRETJSON", "request_json", "reason"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("request payload leaked %q: %s", forbidden, payload)
		}
	}
	for _, want := range []string{`"requestId":"cv-req"`, `"requestKind":"create_task"`, `"state":"pending"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("request payload is missing %s: %s", want, payload)
		}
	}
}

// TestMigrationWorkflowCollaborationCompositeFKTakesPrecedence proves the composite key
// rejects a foreign task even when its id is globally unique, which is the leak a bare FK to
// workflow_tasks(id) would have allowed.
func TestMigrationWorkflowCollaborationCompositeFKTakesPrecedence(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCollaborationVersion)
	now := time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('cf-proj', '/tmp/cf', ?);`, now)
	for _, workflowID := range []string{"cf-w1", "cf-w2"} {
		mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES (?, 'C', 'C', 'active', 1, ?, ?);`, workflowID, now, now)
		mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES (?, ?, 'development', 'A', 'running', 1, ?, ?);`, "cf-task-"+workflowID, workflowID, now, now)
	}

	// A message in w1 naming w2's task: the task id IS globally unique and exists, so only
	// the composite (workflow_id, to_task_id) key can reject this.
	if _, err := db.Exec(`
INSERT INTO workflow_messages (
    id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
) VALUES ('cf-msg', 'cf-w1', 'cf-task-cf-w1', 'cf-task-cf-w2', NULL, NULL,
    'agent', 'cf-task-cf-w1', 'progress', 'persisted', 'hi', '',
    'cf-client', ?, 1, ?, ?);`, strings.Repeat("c", 64), now, now); err == nil {
		t.Fatal("a message referenced a task from another workflow; the composite FK is missing")
	}
}

// TestMigrationWorkflowCollaborationSessionNullable proves session ids accept NULL in every
// delivery state, so ON DELETE SET NULL can always land.
func TestMigrationWorkflowCollaborationSessionNullable(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCollaborationVersion)
	now := time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('sn-proj', '/tmp/sn', ?);`, now)
	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('sn-w', 'S', 'S', 'active', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES ('sn-task', 'sn-w', 'development', 'A', 'running', 1, ?, ?);`, now, now)

	for i, state := range []string{"persisted", "forwarded", "rejected", "unknown"} {
		if _, err := db.Exec(`
INSERT INTO workflow_messages (
    id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
) VALUES (?, 'sn-w', 'sn-task', NULL, NULL, NULL, 'agent', 'sn-task', 'progress', ?, 'x', '',
    ?, ?, 1, ?, ?);`, "sn-msg-"+string(rune('a'+i)), state, "sn-client-"+string(rune('a'+i)),
			strings.Repeat("d", 64), now, now); err != nil {
			t.Fatalf("delivery state %q must accept NULL sessions: %v", state, err)
		}
	}
}

// workflowTaskRequestRead reports whether a Go source file contains a SQL statement that
// touches the workflow_task_requests table.
//
// The distinction matters. This guard exists to prove that no scheduler, recovery loop, or
// daemon path READS the table; it is not a census of the identifier. The MCP tool surface
// names its tools workflow_task_request_get and workflow_task_requests_list, so a guard that
// matched any occurrence of the string would fire on files that reach the table only through
// the HTTP API and contain no SQL at all — which is what happened, and what made the check
// report three false positives.
//
// A real read or write names the table as the object of a SQL verb, so that is what is
// matched: FROM/INTO/UPDATE/JOIN/DELETE FROM followed by the table name. The table name must
// end at a word boundary, which the tool name does not (it continues with "_list"). \s+
// spans newlines, so a statement split across lines is still caught.
func workflowTaskRequestRead(content string) bool {
	return workflowTaskRequestReadPattern.MatchString(content)
}

var workflowTaskRequestReadPattern = regexp.MustCompile(
	`(?is)\b(?:from|into|update|join|delete\s+from)\s+workflow_task_requests\b`)

// TestWorkflowTaskRequestReadMatcher keeps the narrowed guard honest.
//
// The guard above is only meaningful if its matcher still bites. These cases pin both
// directions: a genuine SQL access is flagged however it is spelled, and the MCP tool names
// that produced the false positives are not.
func TestWorkflowTaskRequestReadMatcher(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"select", `const q = "SELECT id FROM workflow_task_requests WHERE workflow_id = ?"`, true},
		{"insert", `INSERT INTO workflow_task_requests (id) VALUES (?)`, true},
		{"update", `UPDATE workflow_task_requests SET state = ? WHERE id = ?`, true},
		{"delete", `DELETE FROM workflow_task_requests WHERE id = ?`, true},
		{"join", `SELECT t.id FROM workflow_tasks t JOIN workflow_task_requests r ON r.id = t.id`, true},
		{"split across lines", "SELECT id\nFROM workflow_task_requests\nWHERE id = ?", true},
		{"lowercase", `select id from workflow_task_requests`, true},
		{"mcp tool name", `"name": "workflow_task_requests_list",`, false},
		{"mcp tool call", `mcpCall("workflow_task_requests_list", nil)`, false},
		{"comment only", `// →  SQLite (workflow_messages, workflow_task_requests)`, false},
		{"unrelated file", `func f() { return }`, false},
	}
	for _, tc := range cases {
		if got := workflowTaskRequestRead(tc.content); got != tc.want {
			t.Errorf("%s: workflowTaskRequestRead = %v, want %v for %q", tc.name, got, tc.want, tc.content)
		}
	}
}

// TestNoSchedulerReadsWorkflowTaskRequests is the APPROVAL-BOUNDARY PROOF required by the
// design: no scheduler, recovery loop, or daemon code path may read workflow_task_requests.
//
// It is a grep test on purpose. A behavioural test can only show that the paths it knows about
// ignore a pending request; a source scan shows that no other path reads it at all, which is
// what "a pending request has no scheduling effect" actually means.
func TestNoSchedulerReadsWorkflowTaskRequests(t *testing.T) {
	// Only the collaboration service, its store, the domain model, and the migration may
	// mention the table. Everything else — every scheduler, recovery loop, reaper, and daemon
	// path — must not read it, which is what "a pending request has no scheduling effect"
	// means in source form.
	allowedSuffixes := []string{
		"service/collaboration/coordinator.go",
		"service/collaboration/coordinator_test.go",
		"storage/sqlite/store/workflow_collaboration_store.go",
		"storage/sqlite/store/workflow_collaboration_store_test.go",
		"storage/sqlite/migrate_workflow_collaboration_test.go",
		"domain/workflow_collaboration.go",
		"domain/workflow_collaboration_test.go",
	}
	// Generated sqlc code is the store's own business.
	allowedPrefixes := []string{"storage/sqlite/gen/"}

	// A Go test's working directory is its package directory
	// (backend/internal/storage/sqlite), so ../.. is backend/internal.
	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !workflowTaskRequestRead(string(content)) {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		normalised := filepath.ToSlash(relative)
		for _, suffix := range allowedSuffixes {
			if strings.HasSuffix(normalised, suffix) {
				return nil
			}
		}
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(normalised, prefix) {
				return nil
			}
		}
		offenders = append(offenders, normalised)
		return nil
	})
	if err != nil {
		t.Fatalf("scan for workflow_task_requests readers: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("these files read workflow_task_requests outside the collaboration boundary: %v", offenders)
	}
}
