package sqlite

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	workflowIdempotencyPreviousVersion int64 = 20261103044048260
	workflowIdempotencyVersion         int64 = 20261103050931629
)

func TestMigrationWorkflowIdempotencyCreatesScopedLedger(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowIdempotencyPreviousVersion)
	if err := migrate(db); err != nil {
		t.Fatalf("apply workflow idempotency migration: %v", err)
	}

	wantColumns := []string{
		"idempotency_key", "workflow_id", "operation", "request_fingerprint",
		"result_type", "result_id", "created_at",
	}
	if got := tableColumns(t, db, "workflow_idempotency"); !reflect.DeepEqual(got, wantColumns) {
		t.Fatalf("workflow_idempotency columns = %v, want %v", got, wantColumns)
	}

	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('workflow-idempotency', 'Test', 'Test idempotency ledger', 'draft', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_idempotency (
    idempotency_key, workflow_id, operation, request_fingerprint,
    result_type, result_id, created_at
) VALUES ('key-1', 'workflow-idempotency', 'workflow.create', ?, 'workflow', 'workflow-idempotency', ?);`,
		strings.Repeat("a", 64), now)

	if _, err := db.Exec(`
INSERT INTO workflow_idempotency (
    idempotency_key, workflow_id, operation, request_fingerprint,
    result_type, result_id, created_at
) VALUES ('key-1', 'workflow-idempotency', 'workflow.create', ?, 'workflow', 'workflow-idempotency', ?)`,
		strings.Repeat("a", 64), now); err == nil {
		t.Fatal("duplicate idempotency key was accepted")
	}
	if _, err := db.Exec(`
INSERT INTO workflow_idempotency (
    idempotency_key, workflow_id, operation, request_fingerprint,
    result_type, result_id, created_at
) VALUES ('key-resolve', 'workflow-idempotency', 'attempt.resolve', ?, 'attempt', 'attempt-1', ?);`,
		strings.Repeat("b", 64), now); err != nil {
		t.Fatalf("reconciliation operation was rejected: %v", err)
	}

	if _, err := db.Exec(`
INSERT INTO workflow_idempotency (
    idempotency_key, workflow_id, operation, request_fingerprint,
    result_type, result_id, created_at
) VALUES ('key-bad-operation', 'workflow-idempotency', 'unknown', ?, 'workflow', 'workflow-idempotency', ?)`,
		strings.Repeat("a", 64), now); err == nil {
		t.Fatal("unknown idempotency operation was accepted")
	}
	if _, err := db.Exec(`
INSERT INTO workflow_idempotency (
    idempotency_key, workflow_id, operation, request_fingerprint,
    result_type, result_id, created_at
) VALUES ('key-bad-result', 'workflow-idempotency', 'workflow.create', ?, 'other', 'workflow-idempotency', ?)`,
		strings.Repeat("a", 64), now); err == nil {
		t.Fatal("unknown idempotency result type was accepted")
	}
	if _, err := db.Exec(`
INSERT INTO workflow_idempotency (
    idempotency_key, workflow_id, operation, request_fingerprint,
    result_type, result_id, created_at
) VALUES ('key-bad-fingerprint', 'workflow-idempotency', 'workflow.create', 'short', 'workflow', 'workflow-idempotency', ?)`, now); err == nil {
		t.Fatal("invalid request fingerprint length was accepted")
	}

	if _, err := db.Exec("DELETE FROM workflows WHERE id = 'workflow-idempotency'"); err != nil {
		t.Fatalf("delete workflow: %v", err)
	}
	var remaining int
	if err := db.QueryRow("SELECT COUNT(*) FROM workflow_idempotency WHERE idempotency_key = 'key-1'").Scan(&remaining); err != nil {
		t.Fatalf("count idempotency rows after workflow delete: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("idempotency rows after workflow delete = %d, want 0", remaining)
	}
}

func TestMigrationWorkflowIdempotencyDownRemovesLedger(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowIdempotencyVersion)
	downTo(t, db, workflowIdempotencyPreviousVersion)

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'workflow_idempotency'`).Scan(&count); err != nil {
		t.Fatalf("inspect workflow_idempotency after rollback: %v", err)
	}
	if count != 0 {
		t.Fatal("workflow_idempotency remains after rolling back its migration")
	}
}
