package sqlite

import (
	"reflect"
	"strings"
	"testing"
)

const (
	workflowAuditPreviousVersion int64 = 20261104020830811
	workflowAuditVersion         int64 = 20261104094706356
)

// TestMigrationWorkflowAuditLogSchema pins the durable audit table's shape.
//
// The schema IS the append-only guarantee for the columns: no UPDATE/DELETE query exists,
// and the shape must stay small enough that no field tempts one (no body, no token).
func TestMigrationWorkflowAuditLogSchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowAuditPreviousVersion)
	if err := migrate(db); err != nil {
		t.Fatalf("apply workflow audit migration: %v", err)
	}

	wantColumns := []string{
		"id", "actor_kind", "actor_ref", "workflow_id", "attempt_id", "request_id",
		"decision", "outcome", "token_fingerprint", "created_at",
	}
	if got := tableColumns(t, db, "workflow_audit_log"); !reflect.DeepEqual(got, wantColumns) {
		t.Fatalf("workflow_audit_log columns = %v, want %v", got, wantColumns)
	}

	// No column may hold the credential itself, a message body, or a prompt.
	for _, column := range wantColumns {
		lower := strings.ToLower(column)
		if lower == "token" || lower == "text" || lower == "body" ||
			strings.Contains(lower, "prompt") || strings.Contains(lower, "content") ||
			strings.Contains(lower, "message_text") {
			t.Fatalf("workflow_audit_log gained a %q column; the trail stores fingerprints only", column)
		}
	}

	// The read path is (workflow, oldest first).
	var indexSQL string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_workflow_audit_log_workflow'`,
	).Scan(&indexSQL); err != nil {
		t.Fatalf("read audit index: %v", err)
	}
	for _, want := range []string{"workflow_id", "created_at"} {
		if !strings.Contains(indexSQL, want) {
			t.Fatalf("audit index does not cover %q: %s", want, indexSQL)
		}
	}

	// No CDC trigger: the audit trail is an operator record, not an event stream, and
	// parallel manual CDC is forbidden.
	var triggers int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND tbl_name='workflow_audit_log'`,
	).Scan(&triggers); err != nil {
		t.Fatalf("count audit triggers: %v", err)
	}
	if triggers != 0 {
		t.Fatalf("workflow_audit_log has %d trigger(s), want 0", triggers)
	}

	// The table is deliberately NOT a foreign-key child: an audit row must outlive the
	// workflow it describes.
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_key_list(workflow_audit_log)`).Scan(new(int), new(int),
		new(string), new(string), new(string), new(string), new(string), new(string)); err == nil {
		foreignKeys = 1
	}
	if foreignKeys != 0 {
		t.Fatal("workflow_audit_log declares a foreign key; deleting a workflow must not erase its audit trail")
	}
}

// TestMigrationWorkflowAuditLogDownRoundTrips proves the rollback drops what it created.
func TestMigrationWorkflowAuditLogDownRoundTrips(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowAuditPreviousVersion)
	if err := migrate(db); err != nil {
		t.Fatalf("apply workflow audit migration: %v", err)
	}
	downTo(t, db, workflowAuditPreviousVersion)
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='workflow_audit_log'`,
	).Scan(&count); err != nil {
		t.Fatalf("count audit table: %v", err)
	}
	if count != 0 {
		t.Fatal("workflow_audit_log survived its rollback")
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_workflow_audit_log_workflow'`,
	).Scan(&count); err != nil {
		t.Fatalf("count audit index: %v", err)
	}
	if count != 0 {
		t.Fatal("the audit index survived its rollback")
	}
}
