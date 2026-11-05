package sqlite

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

const (
	workflowAttemptBindingsPreviousVersion int64 = 20261103073516664
	workflowAttemptBindingsVersion         int64 = 20261103143320688
)

// TestMigrationWorkflowAttemptBindingsSchema pins the shape that the store and the
// migration must agree on: without the session UNIQUE constraint, two attempts could
// silently share one session and later fight over its controller.
func TestMigrationWorkflowAttemptBindingsSchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowAttemptBindingsPreviousVersion)
	if err := migrate(db); err != nil {
		t.Fatalf("apply workflow attempt binding migration: %v", err)
	}

	wantBindingColumns := []string{
		"attempt_id", "workflow_id", "task_id", "spawn_request_id", "spawn_hash",
		"state", "session_id", "controller_generation", "bound_at", "failure_code",
		"revision", "created_at", "updated_at",
	}
	if got := tableColumns(t, db, "workflow_attempt_bindings"); !reflect.DeepEqual(got, wantBindingColumns) {
		t.Fatalf("workflow_attempt_bindings columns = %v, want %v", got, wantBindingColumns)
	}

	wantRepoColumns := []string{
		"attempt_id", "workflow_id", "repo_key", "project_id", "expected_base_sha",
		"session_worktree_ref", "observed_head_sha", "state", "revision", "updated_at",
	}
	if got := tableColumns(t, db, "workflow_attempt_repo_bindings"); !reflect.DeepEqual(got, wantRepoColumns) {
		t.Fatalf("workflow_attempt_repo_bindings columns = %v, want %v", got, wantRepoColumns)
	}

	// The loss trigger must exist: without it a session delete fails the coherence CHECK
	// and a bound attempt pins its session forever.
	var triggerCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='sessions_workflow_binding_loss'`,
	).Scan(&triggerCount); err != nil {
		t.Fatalf("count loss trigger: %v", err)
	}
	if triggerCount != 1 {
		t.Fatal("sessions_workflow_binding_loss is missing; deleting a bound session would fail the CHECK constraint")
	}

	// The lease capability must not be mirrored into the binding tables.
	for _, table := range []string{"workflow_attempt_bindings", "workflow_attempt_repo_bindings"} {
		for _, column := range tableColumns(t, db, table) {
			if strings.Contains(column, "lease") {
				t.Fatalf("%s gained a lease column %q; the capability must stay in workflow_attempts",
					table, column)
			}
		}
	}
}

// TestMigrationWorkflowAttemptBindingsDownRoundTrips proves the migration is
// reversible, which the repo requires of every new migration.
func TestMigrationWorkflowAttemptBindingsDownRoundTrips(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowAttemptBindingsVersion)
	if err := goose.DownTo(db, "migrations", workflowAttemptBindingsPreviousVersion); err != nil {
		t.Fatalf("migrate down to previous version: %v", err)
	}
	for _, table := range []string{"workflow_attempt_bindings", "workflow_attempt_repo_bindings"} {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s survived the down migration", table)
		}
	}
	// The parent attempt index must be gone too, or a later migration would collide.
	var indexCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_workflow_attempts_identity'`,
	).Scan(&indexCount); err != nil {
		t.Fatalf("count identity index: %v", err)
	}
	if indexCount != 0 {
		t.Fatal("idx_workflow_attempts_identity survived the down migration")
	}
	var triggerCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='sessions_workflow_binding_loss'`,
	).Scan(&triggerCount); err != nil {
		t.Fatalf("count loss trigger: %v", err)
	}
	if triggerCount != 0 {
		t.Fatal("the loss trigger survived the down migration")
	}
}

// TestMigrationWorkflowAttemptBindingsConstraints exercises each load-bearing
// constraint directly in SQL, independent of the store layer.
func TestMigrationWorkflowAttemptBindingsConstraints(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowAttemptBindingsVersion)
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)

	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('bind-proj', '/tmp/bind-proj', ?);`, now)
	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('bind-workflow', 'Bind', 'Verify binding constraints', 'active', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_repositories (workflow_id, repo_key, project_id)
VALUES ('bind-workflow', 'backend', 'bind-proj');`, now)
	mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES ('bind-task', 'bind-workflow', 'development', 'Task', 'claimed', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_attempts (
    id, workflow_id, task_id, attempt_no, worker_id, lease_token, lease_until, state, created_at, updated_at
) VALUES ('bind-attempt', 'bind-workflow', 'bind-task', 1, 'worker-1', 'lease-token',
    ?, 'claimed', ?, ?);`, now.Add(time.Hour), now, now)
	// A second attempt on a second task, so the "one session, one binding" case has two
	// legal parents.
	mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES ('bind-task-2', 'bind-workflow', 'development', 'Task 2', 'claimed', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_attempts (
    id, workflow_id, task_id, attempt_no, worker_id, lease_token, lease_until, state, created_at, updated_at
) VALUES ('bind-attempt-2', 'bind-workflow', 'bind-task-2', 1, 'worker-2', 'lease-token-2',
    ?, 'claimed', ?, ?);`, now.Add(time.Hour), now, now)

	insertBinding := func(attemptID, spawnRequest, hashValue, state string) error {
		_, err := db.Exec(`
INSERT INTO workflow_attempt_bindings (
    attempt_id, workflow_id, task_id, spawn_request_id, spawn_hash, state,
    session_id, controller_generation, bound_at, failure_code, revision, created_at, updated_at
) VALUES (?, 'bind-workflow', CASE WHEN ? = 'bind-attempt' THEN 'bind-task' ELSE 'bind-task-2' END,
    ?, ?, ?, NULL, '', NULL, '', 1, ?, ?);`,
			attemptID, attemptID, spawnRequest, hashValue, state, now, now)
		return err
	}

	if err := insertBinding("bind-attempt", "spawn-1", hash, "intent_persisted"); err != nil {
		t.Fatalf("insert binding: %v", err)
	}

	// spawn_request_id UNIQUE is the idempotent-spawn guarantee.
	if err := insertBinding("bind-attempt-2", "spawn-1", hash, "intent_persisted"); err == nil {
		t.Fatal("a duplicate spawn_request_id was accepted")
	}
	// A spawn hash must be a 64-character fingerprint.
	if err := insertBinding("bind-attempt-2", "spawn-2", "deadbeef", "intent_persisted"); err == nil {
		t.Fatal("a short spawn_hash was accepted")
	}
	// The state set is closed.
	if err := insertBinding("bind-attempt-2", "spawn-3", hash, "spawning"); err == nil {
		t.Fatal("an unknown binding state was accepted")
	}
	// A pre-session state must not claim a session.
	if _, err := db.Exec(`
UPDATE workflow_attempt_bindings SET session_id = 'p1-1' WHERE attempt_id = 'bind-attempt';`); err == nil {
		t.Fatal("a pre-session state accepted a session id")
	}
	// A session-bearing state must name its session and its bound_at.
	if _, err := db.Exec(`
UPDATE workflow_attempt_bindings SET state = 'bound_ready' WHERE attempt_id = 'bind-attempt';`); err == nil {
		t.Fatal("bound_ready was accepted without a session")
	}

	// Insert the session rows the binding needs.
	for i, sessionID := range []string{"p1-1", "p1-2"} {
		mustExec(t, db, `
INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, is_terminated, created_at, updated_at)
VALUES (?, 'bind-proj', ?, 'worker', 'exited', ?, 0, ?, ?);`, sessionID, i+1, now, now, now)
	}

	mustExec(t, db, `
UPDATE workflow_attempt_bindings
SET session_id = 'p1-1', bound_at = ?, state = 'bound_ready'
WHERE attempt_id = 'bind-attempt';`, now)

	// session_id UNIQUE is the "one session belongs to one attempt" guarantee.
	if err := insertBinding("bind-attempt-2", "spawn-4", hash, "intent_persisted"); err != nil {
		t.Fatalf("insert second binding: %v", err)
	}
	if _, err := db.Exec(`
UPDATE workflow_attempt_bindings
SET session_id = 'p1-1', bound_at = ?, state = 'bound_ready'
WHERE attempt_id = 'bind-attempt-2';`, now); err == nil {
		t.Fatal("the same session was bound to two attempts")
	}
}

// TestMigrationWorkflowAttemptRepoBindingCrossWorkflowFK proves cross-workflow
// rejection is a real foreign key rather than a store convention: a repo_key the
// workflow never registered cannot be inserted at all.
func TestMigrationWorkflowAttemptRepoBindingCrossWorkflowFK(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowAttemptBindingsVersion)
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	hash := strings.Repeat("b", 64)

	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('fk-proj', '/tmp/fk-proj', ?);`, now)
	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('fk-workflow', 'FK', 'Verify repo foreign key', 'active', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_repositories (workflow_id, repo_key, project_id)
VALUES ('fk-workflow', 'backend', 'fk-proj');`, now)
	mustExec(t, db, `
INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, revision, created_at, updated_at)
VALUES ('fk-task', 'fk-workflow', 'development', 'Task', 'claimed', 1, ?, ?);`, now, now)
	mustExec(t, db, `
INSERT INTO workflow_attempts (
    id, workflow_id, task_id, attempt_no, worker_id, lease_token, lease_until, state, created_at, updated_at
) VALUES ('fk-attempt', 'fk-workflow', 'fk-task', 1, 'worker-1', 'lease-token',
    ?, 'claimed', ?, ?);`, now.Add(time.Hour), now, now)
	mustExec(t, db, `
INSERT INTO workflow_attempt_bindings (
    attempt_id, workflow_id, task_id, spawn_request_id, spawn_hash, state,
    session_id, controller_generation, bound_at, failure_code, revision, created_at, updated_at
) VALUES ('fk-attempt', 'fk-workflow', 'fk-task', 'fk-spawn', ?, 'intent_persisted',
    NULL, '', NULL, '', 1, ?, ?);`, hash, now, now)

	insertRepo := func(repoKey string) error {
		_, err := db.Exec(`
INSERT INTO workflow_attempt_repo_bindings (
    attempt_id, workflow_id, repo_key, project_id, expected_base_sha,
    session_worktree_ref, observed_head_sha, state, revision, updated_at
) VALUES ('fk-attempt', 'fk-workflow', ?, 'fk-proj', 'base', '', '', 'pending', 1, ?);`, repoKey, now)
		return err
	}

	if err := insertRepo("backend"); err != nil {
		t.Fatalf("insert registered repo binding: %v", err)
	}
	// "frontend" was never registered on this workflow.
	if err := insertRepo("frontend"); err == nil {
		t.Fatal("a repo binding for an unregistered repository was accepted; the composite FK is not enforcing cross-workflow rejection")
	}

	// Deleting the binding cascades its repo rows: a binding owns its expectations.
	mustExec(t, db, `DELETE FROM workflow_attempt_bindings WHERE attempt_id = 'fk-attempt';`)
	var remaining int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM workflow_attempt_repo_bindings WHERE attempt_id = 'fk-attempt'`,
	).Scan(&remaining); err != nil {
		t.Fatalf("count repo bindings: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("repo bindings survived their parent binding: %d", remaining)
	}
}
