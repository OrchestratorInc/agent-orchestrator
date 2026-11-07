package sqlite

import (
	"reflect"
	"testing"
	"time"
)

const (
	workflowCorePreviousVersion int64 = 20261102100313400
	workflowCoreVersion         int64 = 20261103044048260
)

func TestMigrationWorkflowCoreCreatesDurableTablesAndConstraints(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCorePreviousVersion)
	if err := migrate(db); err != nil {
		t.Fatalf("apply workflow core migration: %v", err)
	}

	wantColumns := map[string][]string{
		"workflows": {
			"id", "title", "goal", "owner_session_id", "state", "revision", "created_at", "updated_at",
		},
		"workflow_repositories": {
			"workflow_id", "repo_key", "project_id", "path_ref", "base_sha", "target_ref",
		},
		"workflow_tasks": {
			"id", "workflow_id", "kind", "title", "brief", "state", "priority", "agent_harness",
			"max_attempts", "revision", "created_at", "updated_at", "model", "effort", "profile", "planner",
		},
		"workflow_edges":      {"workflow_id", "from_task_id", "to_task_id"},
		"workflow_task_repos": {"workflow_id", "task_id", "repo_key", "allowed_paths_json", "base_sha"},
		"workflow_attempts": {
			"id", "workflow_id", "task_id", "attempt_no", "session_id", "worker_id", "lease_token",
			"lease_until", "heartbeat_at", "state", "started_at", "ended_at", "error_code",
			"created_at", "updated_at",
		},
	}
	for table, want := range wantColumns {
		if got := tableColumns(t, db, table); !reflect.DeepEqual(got, want) {
			t.Errorf("%s columns = %v, want %v", table, got, want)
		}
	}

	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `
INSERT INTO projects (id, path, display_name, registered_at)
VALUES ('workflow-project', '/tmp/workflow-project', 'Workflow project', ?);
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('workflow-1', 'M1', 'Implement workflow core', 'draft', 1, ?, ?);
INSERT INTO workflow_repositories (
    workflow_id, repo_key, project_id, path_ref, base_sha, target_ref
) VALUES ('workflow-1', 'backend', 'workflow-project', '', 'base-sha', 'main');
INSERT INTO workflow_tasks (
    id, workflow_id, kind, title, brief, state, priority, max_attempts, revision, created_at, updated_at
) VALUES
    ('task-1', 'workflow-1', 'development', 'Implement schema', '', 'pending', 10, 2, 1, ?, ?),
    ('task-2', 'workflow-1', 'testing', 'Verify schema', '', 'pending', 5, 1, 1, ?, ?);
INSERT INTO workflow_edges (workflow_id, from_task_id, to_task_id)
VALUES ('workflow-1', 'task-1', 'task-2');
INSERT INTO workflow_task_repos (
    workflow_id, task_id, repo_key, allowed_paths_json, base_sha
) VALUES ('workflow-1', 'task-1', 'backend', '["backend/internal"]', 'base-sha');
INSERT INTO workflow_attempts (
    id, workflow_id, task_id, attempt_no, worker_id, lease_token, lease_until,
    state, created_at, updated_at
) VALUES ('attempt-1', 'workflow-1', 'task-1', 1, 'local-test', 'lease-1', ?, 'claimed', ?, ?);`,
		now, now, now, now, now, now, now, now, now, now)

	// Composite foreign keys prevent an edge or task-repo binding from crossing
	// workflow boundaries even when the referenced task/repo key exists elsewhere.
	mustExec(t, db, `
INSERT INTO workflows (id, title, goal, state, revision, created_at, updated_at)
VALUES ('workflow-2', 'Other', 'Other graph', 'draft', 1, ?, ?);
INSERT INTO workflow_tasks (
    id, workflow_id, kind, title, state, max_attempts, revision, created_at, updated_at
) VALUES ('task-other', 'workflow-2', 'development', 'Other task', 'pending', 1, 1, ?, ?);`,
		now, now, now, now)

	if _, err := db.Exec(`INSERT INTO workflow_edges (workflow_id, from_task_id, to_task_id) VALUES ('workflow-1', 'task-1', 'task-1')`); err == nil {
		t.Fatal("self dependency was accepted")
	}
	if _, err := db.Exec(`INSERT INTO workflow_edges (workflow_id, from_task_id, to_task_id) VALUES ('workflow-1', 'task-1', 'task-other')`); err == nil {
		t.Fatal("cross-workflow dependency was accepted")
	}
	if _, err := db.Exec(`INSERT INTO workflow_task_repos (workflow_id, task_id, repo_key, allowed_paths_json) VALUES ('workflow-1', 'task-1', 'missing-repo', '[]')`); err == nil {
		t.Fatal("task binding to an unregistered workflow repository was accepted")
	}
	if _, err := db.Exec(`INSERT INTO workflow_task_repos (workflow_id, task_id, repo_key, allowed_paths_json) VALUES ('workflow-1', 'task-1', 'backend', '{not-json}')`); err == nil {
		t.Fatal("invalid task-repository path JSON was accepted")
	}
	if _, err := db.Exec(`INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, max_attempts, revision, created_at, updated_at) VALUES ('task-invalid', 'workflow-1', 'unknown', 'Bad kind', 'pending', 1, 1, ?, ?)`, now, now); err == nil {
		t.Fatal("unknown task kind was accepted")
	}
	if _, err := db.Exec(`INSERT INTO workflow_tasks (id, workflow_id, kind, title, state, max_attempts, revision, created_at, updated_at) VALUES ('task-invalid-state', 'workflow-1', 'development', 'Bad state', 'queued', 1, 1, ?, ?)`, now, now); err == nil {
		t.Fatal("unknown task state was accepted")
	}
	if _, err := db.Exec(`INSERT INTO workflow_attempts (id, workflow_id, task_id, attempt_no, state, created_at, updated_at) VALUES ('attempt-no-lease', 'workflow-1', 'task-1', 2, 'claimed', ?, ?)`, now, now); err == nil {
		t.Fatal("claimed attempt without a lease was accepted")
	}
	if _, err := db.Exec(`INSERT INTO workflow_attempts (id, workflow_id, task_id, attempt_no, state, created_at, updated_at) VALUES ('attempt-duplicate-number', 'workflow-1', 'task-1', 1, 'failed', ?, ?)`, now, now); err == nil {
		t.Fatal("duplicate task attempt number was accepted")
	}
	if _, err := db.Exec(`INSERT INTO workflow_attempts (id, workflow_id, task_id, attempt_no, worker_id, lease_token, lease_until, state, created_at, updated_at) VALUES ('attempt-second-active', 'workflow-1', 'task-1', 2, 'local-test', 'lease-2', ?, 'claimed', ?, ?)`, now.Add(time.Minute), now, now); err == nil {
		t.Fatal("second active attempt for the same task was accepted")
	}
}

func TestMigrationWorkflowCoreDownRemovesWorkflowTables(t *testing.T) {
	db := openMigratedDatabaseCopy(t, workflowCoreVersion)
	downTo(t, db, workflowCorePreviousVersion)

	for _, table := range []string{
		"workflows", "workflow_repositories", "workflow_tasks", "workflow_edges",
		"workflow_task_repos", "workflow_attempts",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("inspect %s after rollback: %v", table, err)
		}
		if count != 0 {
			t.Errorf("%s remains after rolling back workflow core migration", table)
		}
	}
}
