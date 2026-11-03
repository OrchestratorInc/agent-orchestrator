-- name: InsertWorkflow :exec
INSERT INTO workflows (
    id, title, goal, owner_session_id, state, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetWorkflow :one
SELECT id, title, goal, owner_session_id, state, revision, created_at, updated_at
FROM workflows
WHERE id = ?;

-- name: ListWorkflows :many
SELECT id, title, goal, owner_session_id, state, revision, created_at, updated_at
FROM workflows
WHERE (CAST(sqlc.arg(state_filter) AS TEXT) = '' OR state = CAST(sqlc.arg(state_filter) AS TEXT))
ORDER BY updated_at DESC, id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: InsertWorkflowRepository :exec
INSERT INTO workflow_repositories (
    workflow_id, repo_key, project_id, path_ref, base_sha, target_ref
) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListWorkflowRepositories :many
SELECT workflow_id, repo_key, project_id, path_ref, base_sha, target_ref
FROM workflow_repositories
WHERE workflow_id = ?
ORDER BY repo_key;

-- name: InsertWorkflowTask :exec
INSERT INTO workflow_tasks (
    id, workflow_id, kind, title, brief, state, priority, agent_harness,
    max_attempts, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetWorkflowTask :one
SELECT id, workflow_id, kind, title, brief, state, priority, agent_harness,
    max_attempts, revision, created_at, updated_at
FROM workflow_tasks
WHERE workflow_id = ? AND id = ?;

-- name: ListWorkflowTasks :many
SELECT id, workflow_id, kind, title, brief, state, priority, agent_harness,
    max_attempts, revision, created_at, updated_at
FROM workflow_tasks
WHERE workflow_id = sqlc.arg(workflow_id)
  AND (CAST(sqlc.arg(state_filter) AS TEXT) = '' OR state = CAST(sqlc.arg(state_filter) AS TEXT))
ORDER BY priority DESC, created_at, id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: InsertWorkflowEdge :exec
INSERT INTO workflow_edges (workflow_id, from_task_id, to_task_id)
VALUES (?, ?, ?);

-- name: ListWorkflowEdges :many
SELECT workflow_id, from_task_id, to_task_id
FROM workflow_edges
WHERE workflow_id = ?
ORDER BY from_task_id, to_task_id;

-- name: InsertWorkflowTaskRepository :exec
INSERT INTO workflow_task_repos (
    workflow_id, task_id, repo_key, allowed_paths_json, base_sha
) VALUES (?, ?, ?, ?, ?);

-- name: ListWorkflowTaskRepositories :many
SELECT workflow_id, task_id, repo_key, allowed_paths_json, base_sha
FROM workflow_task_repos
WHERE workflow_id = ? AND task_id = ?
ORDER BY repo_key;

-- name: InsertWorkflowAttempt :exec
INSERT INTO workflow_attempts (
    id, workflow_id, task_id, attempt_no, session_id, worker_id, lease_token,
    lease_until, heartbeat_at, state, started_at, ended_at, error_code,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetWorkflowAttempt :one
SELECT id, workflow_id, task_id, attempt_no, session_id, worker_id, lease_token,
    lease_until, heartbeat_at, state, started_at, ended_at, error_code,
    created_at, updated_at
FROM workflow_attempts
WHERE id = ?;

-- name: ListWorkflowAttempts :many
SELECT id, workflow_id, task_id, attempt_no, session_id, worker_id, lease_token,
    lease_until, heartbeat_at, state, started_at, ended_at, error_code,
    created_at, updated_at
FROM workflow_attempts
WHERE workflow_id = ? AND task_id = ?
ORDER BY attempt_no DESC;

-- name: ActivateWorkflow :execrows
UPDATE workflows
SET state = 'active', revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE workflows.id = sqlc.arg(workflow_id)
  AND workflows.state = 'draft'
  AND workflows.revision = sqlc.arg(expected_revision)
  AND EXISTS (
      SELECT 1 FROM workflow_tasks
      WHERE workflow_tasks.workflow_id = workflows.id
  );

-- name: RefreshWorkflowReadyTasks :execrows
UPDATE workflow_tasks
SET state = 'ready', revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE workflow_tasks.workflow_id = sqlc.arg(workflow_id)
  AND workflow_tasks.state = 'pending'
  AND EXISTS (
      SELECT 1 FROM workflows
      WHERE id = sqlc.arg(workflow_id) AND state = 'active'
  )
  AND NOT EXISTS (
      SELECT 1
      FROM workflow_edges e
      JOIN workflow_tasks predecessor
        ON predecessor.workflow_id = e.workflow_id AND predecessor.id = e.from_task_id
      WHERE e.workflow_id = sqlc.arg(workflow_id)
        AND e.to_task_id = workflow_tasks.id
        AND predecessor.state <> 'succeeded'
  )
  AND NOT EXISTS (
      SELECT 1 FROM workflow_attempts a
      WHERE a.workflow_id = workflow_tasks.workflow_id
        AND a.task_id = workflow_tasks.id
        AND a.state IN ('claimed', 'running', 'settling', 'unknown')
  );

-- name: ClaimWorkflowTask :execrows
UPDATE workflow_tasks
SET state = 'claimed', revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE workflow_tasks.workflow_id = sqlc.arg(workflow_id)
  AND workflow_tasks.id = sqlc.arg(task_id)
  AND workflow_tasks.state = 'ready'
  AND workflow_tasks.revision = sqlc.arg(expected_revision)
  AND EXISTS (
      SELECT 1 FROM workflows
      WHERE id = sqlc.arg(workflow_id) AND state = 'active'
  )
  AND NOT EXISTS (
      SELECT 1
      FROM workflow_edges e
      JOIN workflow_tasks predecessor
        ON predecessor.workflow_id = e.workflow_id AND predecessor.id = e.from_task_id
      WHERE e.workflow_id = sqlc.arg(workflow_id)
        AND e.to_task_id = workflow_tasks.id
        AND predecessor.state <> 'succeeded'
  )
  AND (
      SELECT COUNT(*) FROM workflow_attempts a
      WHERE a.workflow_id = workflow_tasks.workflow_id AND a.task_id = workflow_tasks.id
  ) < workflow_tasks.max_attempts
  AND NOT EXISTS (
      SELECT 1 FROM workflow_attempts a
      WHERE a.workflow_id = workflow_tasks.workflow_id
        AND a.task_id = workflow_tasks.id
        AND a.state IN ('claimed', 'running', 'settling', 'unknown')
  );

-- name: GetNextWorkflowAttemptNo :one
SELECT COALESCE(MAX(attempt_no), 0) + 1 AS next_attempt_no
FROM workflow_attempts
WHERE workflow_id = sqlc.arg(workflow_id) AND task_id = sqlc.arg(task_id);

-- name: UpdateWorkflowTaskState :execrows
UPDATE workflow_tasks
SET state = sqlc.arg(next_state), revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE workflow_tasks.workflow_id = sqlc.arg(workflow_id)
  AND workflow_tasks.id = sqlc.arg(task_id)
  AND state = sqlc.arg(expected_state)
  AND revision = sqlc.arg(expected_revision);

-- name: StartWorkflowAttempt :execrows
-- Deliberately NOT gated on workflows.state = 'active': claim and retry are the
-- gates that admit NEW work. A claim granted while the workflow was active must
-- still be startable and settleable (drain semantics) even if a sibling task's
-- terminal failure already moved the workflow to 'failed'.
UPDATE workflow_attempts
SET state = 'running', heartbeat_at = sqlc.arg(now),
    started_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE workflow_attempts.id = sqlc.arg(id)
  AND workflow_attempts.workflow_id = sqlc.arg(workflow_id)
  AND workflow_attempts.state = 'claimed'
  AND workflow_attempts.lease_token = sqlc.arg(lease_token)
  AND lease_until > sqlc.arg(now);

-- name: SettleWorkflowAttempt :execrows
UPDATE workflow_attempts
SET state = sqlc.arg(next_state), lease_token = '', lease_until = NULL,
    ended_at = sqlc.arg(now), error_code = sqlc.arg(error_code), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND workflow_id = sqlc.arg(workflow_id)
  AND state IN ('claimed', 'running')
  AND lease_token = sqlc.arg(lease_token)
  AND lease_until > sqlc.arg(now);

-- name: RetryWorkflowTask :execrows
UPDATE workflow_tasks
SET state = 'ready', revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE workflow_tasks.workflow_id = sqlc.arg(workflow_id)
  AND workflow_tasks.id = sqlc.arg(task_id)
  AND workflow_tasks.state = 'retry_wait'
  AND workflow_tasks.revision = sqlc.arg(expected_revision)
  AND EXISTS (
      SELECT 1 FROM workflows
      WHERE id = sqlc.arg(workflow_id) AND state = 'active'
  )
  AND (
      SELECT COUNT(*) FROM workflow_attempts a
      WHERE a.workflow_id = workflow_tasks.workflow_id AND a.task_id = workflow_tasks.id
  ) < workflow_tasks.max_attempts
  AND NOT EXISTS (
      SELECT 1
      FROM workflow_edges e
      JOIN workflow_tasks predecessor
        ON predecessor.workflow_id = e.workflow_id AND predecessor.id = e.from_task_id
      WHERE e.workflow_id = sqlc.arg(workflow_id)
        AND e.to_task_id = workflow_tasks.id
        AND predecessor.state <> 'succeeded'
  )
  AND NOT EXISTS (
      SELECT 1 FROM workflow_attempts a
      WHERE a.workflow_id = workflow_tasks.workflow_id
        AND a.task_id = workflow_tasks.id
        AND a.state IN ('claimed', 'running', 'settling', 'unknown')
  );

-- name: ResolveUnknownWorkflowAttempt :execrows
-- Reconciliation is the only exit from UNKNOWN, and it never re-dispatches:
-- the UPDATE records the outcome an operator (or reconciliation job) decided,
-- which also removes the attempt from the active-attempt partial index so the
-- task can move again.
UPDATE workflow_attempts
SET state = sqlc.arg(next_state), lease_token = '', lease_until = NULL,
    ended_at = sqlc.arg(now), error_code = sqlc.arg(error_code), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND workflow_id = sqlc.arg(workflow_id)
  AND state = 'unknown';

-- name: MarkExpiredWorkflowAttemptsUnknown :execrows
UPDATE workflow_attempts
SET state = 'unknown', lease_token = '', lease_until = NULL,
    ended_at = sqlc.arg(now), error_code = 'lease_expired_outcome_unknown',
    updated_at = sqlc.arg(now)
WHERE state IN ('claimed', 'running', 'settling')
  AND lease_until IS NOT NULL
  AND lease_until <= sqlc.arg(now);

-- name: BlockTasksWithUnknownWorkflowAttempts :execrows
UPDATE workflow_tasks
SET state = 'blocked', revision = revision + 1, updated_at = sqlc.arg(now)
WHERE state IN ('claimed', 'running', 'verifying')
  AND EXISTS (
      SELECT 1 FROM workflow_attempts a
      WHERE a.workflow_id = workflow_tasks.workflow_id
        AND a.task_id = workflow_tasks.id
        AND a.state = 'unknown'
  );

-- name: CompleteWorkflowIfAllTasksSucceeded :execrows
UPDATE workflows
SET state = 'completed', revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE workflows.id = sqlc.arg(workflow_id)
  AND workflows.state = 'active'
  AND EXISTS (
      SELECT 1 FROM workflow_tasks
      WHERE workflow_tasks.workflow_id = workflows.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM workflow_tasks
      WHERE workflow_tasks.workflow_id = workflows.id
        AND workflow_tasks.state <> 'succeeded'
  );

-- name: FailWorkflowIfAnyTaskFailed :execrows
UPDATE workflows
SET state = 'failed', revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE workflows.id = sqlc.arg(workflow_id)
  AND workflows.state = 'active'
  AND EXISTS (
      SELECT 1 FROM workflow_tasks
      WHERE workflow_tasks.workflow_id = workflows.id
        AND workflow_tasks.state = 'failed'
  );

-- name: InsertWorkflowIdempotency :execrows
INSERT INTO workflow_idempotency (
    idempotency_key, workflow_id, operation, request_fingerprint,
    result_type, result_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetWorkflowIdempotency :one
SELECT idempotency_key, workflow_id, operation, request_fingerprint,
    result_type, result_id, created_at
FROM workflow_idempotency
WHERE idempotency_key = ?;
