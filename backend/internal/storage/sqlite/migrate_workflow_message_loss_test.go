package sqlite

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

const (
	workflowMessageLossPreviousVersion int64 = 20261103163208174
	workflowMessageLossVersion         int64 = 20261103170009848
)

// seedMessageSessionFixtures inserts a workflow, two tasks, and two sessions so the loss
// trigger's two branches can be exercised independently.
func seedMessageSessionFixtures(t *testing.T, db *sql.DB) time.Time {
	t.Helper()
	now := time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('ml-proj', '/tmp/ml', ?);`, now)
	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('ml-w', 'M', 'Loss', 'active', 1, ?, ?);`, now, now)
	for _, taskID := range []string{"ml-task-a", "ml-task-b"} {
		mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES (?, 'ml-w', 'development', 'T', 'running', 1, ?, ?);`, taskID, now, now)
	}
	// change_log.session_id has a RESTRICT-style reference, so the store's delete path clears
	// those rows first; do the same here so this test measures the loss trigger, not the FK.
	mustExec(t, db, `
INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, is_terminated, created_at, updated_at)
VALUES ('ml-sender', 'ml-proj', 1, 'worker', 'exited', ?, 1, ?, ?);`, now, now, now)
	mustExec(t, db, `
INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, is_terminated, created_at, updated_at)
VALUES ('ml-target', 'ml-proj', 2, 'worker', 'exited', ?, 1, ?, ?);`, now, now, now)
	return now
}

// TestMigrationWorkflowMessageLossNullsOnlyTheMatchingColumn is P1-B's proof: deleting the
// sender's session must leave the target reference intact, and vice versa.
//
// The original trigger nulled both columns on any match, so deleting a sender destroyed the
// still-live target reference on that row.
func TestMigrationWorkflowMessageLossNullsOnlyTheMatchingColumn(t *testing.T) {
	t.Run("deleting the sender keeps the target reference", func(t *testing.T) {
		db := openMigratedDatabaseCopy(t, workflowMessageLossVersion)
		now := seedMessageSessionFixtures(t, db)

		mustExec(t, db, `
INSERT INTO workflow_messages (
    id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
) VALUES ('ml-msg-1', 'ml-w', 'ml-task-a', 'ml-task-b', 'ml-sender', 'ml-target',
    'agent', 'ml-task-a', 'progress', 'persisted', 'hi', '',
    'ml-client-1', ?, 1, ?, ?);`, strings.Repeat("a", 64), now, now)

		mustExec(t, db, `DELETE FROM change_log WHERE session_id IN ('ml-sender', 'ml-target');`)
		mustExec(t, db, `DELETE FROM sessions WHERE id = 'ml-sender';`)

		var sender, target *string
		if err := db.QueryRow(
			`SELECT sender_session_id, target_session_id FROM workflow_messages WHERE id = 'ml-msg-1'`,
		).Scan(&sender, &target); err != nil {
			t.Fatalf("read message: %v", err)
		}
		if sender != nil {
			t.Fatalf("sender_session_id = %q, want NULL after its session was deleted", *sender)
		}
		if target == nil {
			t.Fatal("target_session_id was nulled too; deleting the sender destroyed a live reference")
		}
		if *target != "ml-target" {
			t.Fatalf("target_session_id = %q, want the untouched 'ml-target'", *target)
		}
	})

	t.Run("deleting the target keeps the sender reference", func(t *testing.T) {
		db := openMigratedDatabaseCopy(t, workflowMessageLossVersion)
		now := seedMessageSessionFixtures(t, db)

		mustExec(t, db, `
INSERT INTO workflow_messages (
    id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
) VALUES ('ml-msg-2', 'ml-w', 'ml-task-a', 'ml-task-b', 'ml-sender', 'ml-target',
    'agent', 'ml-task-a', 'progress', 'persisted', 'hi', '',
    'ml-client-2', ?, 1, ?, ?);`, strings.Repeat("b", 64), now, now)

		mustExec(t, db, `DELETE FROM change_log WHERE session_id IN ('ml-sender', 'ml-target');`)
		mustExec(t, db, `DELETE FROM sessions WHERE id = 'ml-target';`)

		var sender, target *string
		if err := db.QueryRow(
			`SELECT sender_session_id, target_session_id FROM workflow_messages WHERE id = 'ml-msg-2'`,
		).Scan(&sender, &target); err != nil {
			t.Fatalf("read message: %v", err)
		}
		if target != nil {
			t.Fatalf("target_session_id = %q, want NULL after its session was deleted", *target)
		}
		if sender == nil {
			t.Fatal("sender_session_id was nulled too; deleting the target destroyed a live reference")
		}
		if *sender != "ml-sender" {
			t.Fatalf("sender_session_id = %q, want the untouched 'ml-sender'", *sender)
		}
	})
}

// TestMigrationWorkflowMessagePayloadRefClosedClass is P1-C's proof: the enforced character
// class matches domain.ValidPayloadRef exactly, over the forms the audit listed.
func TestMigrationWorkflowMessagePayloadRefClosedClass(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowMessageLossVersion)
	now := seedMessageSessionFixtures(t, db)

	tests := []struct {
		ref     string
		allowed bool
		why     string
	}{
		{"artifact-abc123", true, "flat alphanumeric id"},
		{"A_b.c-9", true, "all four allowed punctuation characters"},
		{"a..b", true, "a double dot inside a name is a legal flat id"},
		{"", true, "empty is allowed when a body is present"},
		{".", false, "the current directory"},
		{"..", false, "the parent directory"},
		{"a/b", false, "forward slash"},
		{`a\b`, false, "backslash"},
		{"a:b", false, "drive/stream separator"},
		{"a%2Fb", false, "percent-encoded slash"},
		{"a%5Cb", false, "percent-encoded backslash"},
		{"a%252Fb", false, "double-encoded slash"},
		{"a%3Ab", false, "percent-encoded colon"},
		{"CON", true, "a Windows device name is a legal flat id here; resolving it is the consumer's job"},
		{"a b", false, "space is outside the class"},
		{"a\x00b", false, "NUL"},
	}
	for i, tt := range tests {
		t.Run(tt.ref+" "+tt.why, func(t *testing.T) {
			body := "text"
			if tt.ref == "" {
				body = "text"
			}
			_, err := db.Exec(`
INSERT INTO workflow_messages (
    id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
) VALUES (?, 'ml-w', 'ml-task-a', NULL, NULL, NULL, 'agent', 'ml-task-a', 'progress',
    'persisted', ?, ?, ?, ?, 1, ?, ?);`,
				"pr-msg-"+string(rune('a'+i)), body, tt.ref, "pr-client-"+string(rune('a'+i)),
				strings.Repeat("c", 64), now, now)
			if tt.allowed && err != nil {
				t.Fatalf("the CHECK rejected a legal ref %q: %v", tt.ref, err)
			}
			if !tt.allowed && err == nil {
				t.Fatalf("the CHECK accepted a path-like ref %q (%s)", tt.ref, tt.why)
			}
		})
	}
}
