-- Attempt-to-session binding queries (M2).
--
-- These statements never read or write workflow_attempts.lease_token. The lease
-- capability stays in workflow_attempts so that exactly one response may return it;
-- a binding read is safe to hand to any authorized reader.

-- name: GetWorkflowAttemptBinding :one
SELECT attempt_id, workflow_id, task_id, spawn_request_id, spawn_hash, state,
    session_id, controller_generation, bound_at, failure_code, revision,
    created_at, updated_at
FROM workflow_attempt_bindings
WHERE attempt_id = ?;

-- name: GetWorkflowAttemptBindingBySpawnRequest :one
SELECT attempt_id, workflow_id, task_id, spawn_request_id, spawn_hash, state,
    session_id, controller_generation, bound_at, failure_code, revision,
    created_at, updated_at
FROM workflow_attempt_bindings
WHERE spawn_request_id = ?;

-- name: GetWorkflowAttemptBindingBySession :one
-- The session-side lookup. A resumed session has no spawn request in hand, so this is
-- how it recovers which attempt it belongs to. session_id is unique per binding (a
-- session is bound to at most one attempt), so this cannot fan out.
SELECT attempt_id, workflow_id, task_id, spawn_request_id, spawn_hash, state,
    session_id, controller_generation, bound_at, failure_code, revision,
    created_at, updated_at
FROM workflow_attempt_bindings
WHERE session_id = ?;

-- name: InsertWorkflowAttemptBinding :execrows
-- The spawn_request_id UNIQUE constraint is the idempotency guarantee: a second
-- insert either fails or is reported as a replay by the caller's lookup, never as a
-- second binding.
INSERT INTO workflow_attempt_bindings (
    attempt_id, workflow_id, task_id, spawn_request_id, spawn_hash, state,
    session_id, controller_generation, bound_at, failure_code, revision,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: UpdateWorkflowAttemptBindingState :execrows
-- A plain state CAS used only for transitions that do not touch the session: the
-- caller supplies the full expected revision, so two reconcilers cannot both apply
-- different outcomes.
UPDATE workflow_attempt_bindings
SET state = sqlc.arg(next_state), failure_code = sqlc.arg(failure_code),
    revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE attempt_id = sqlc.arg(attempt_id)
  AND revision = sqlc.arg(expected_revision)
  AND state = sqlc.arg(expected_state);

-- name: BindWorkflowAttemptSession :execrows
-- The session-bearing CAS. It refuses to overwrite an existing session id so a second
-- bind cannot silently re-point the binding. The target state is supplied by the caller
-- (session_provisioning or bound_ready) so this statement can express both "a session
-- exists but is not ready yet" and "the session is ready", without ever expressing
-- running: promotion to running needs runtime evidence, not a session row.
UPDATE workflow_attempt_bindings
SET session_id = sqlc.arg(session_id),
    controller_generation = sqlc.arg(controller_generation),
    bound_at = sqlc.arg(bound_at),
    state = sqlc.arg(next_state),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE attempt_id = sqlc.arg(attempt_id)
  AND revision = sqlc.arg(expected_revision)
  AND state = sqlc.arg(expected_state)
  AND session_id IS NULL;

-- name: ReanchorWorkflowAttemptGeneration :execrows
-- The controller-replacement CAS. It moves ONLY the recorded generation: the session id
-- is a predicate, never an assignment, so a re-anchor can never re-point a binding at a
-- different session. A chat controller that replaces the one a binding recorded rotates
-- its generation, and without this statement the binding would keep the dead generation
-- forever, which makes every fence against the live controller refuse.
--
-- The caller passes the exact revision and state it read, so two observations of the same
-- replacement cannot both write, and a binding that moved on (stopped, failed,
-- reconciling) is left alone rather than re-anchored under a stale assumption.
--
-- Only states that legitimately hold a live session may be passed as expected_state.
-- The coordinator uses this for the states an observation can land on with no forward
-- edge in the graph, bound_ready above all, where bound_ready to bound_ready does not
-- exist. Nothing here can promote, demote, clear, or re-point a session.
UPDATE workflow_attempt_bindings
SET controller_generation = sqlc.arg(controller_generation),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE attempt_id = sqlc.arg(attempt_id)
  AND revision = sqlc.arg(expected_revision)
  AND state = sqlc.arg(expected_state)
  AND session_id = sqlc.arg(session_id);

-- name: SetWorkflowAttemptSession :execrows
-- Mirrors the binding's session onto the M1 attempt row in the same transaction.
-- Only an attempt with no session yet may be written, so the two truths cannot be
-- established twice.
UPDATE workflow_attempts
SET session_id = sqlc.arg(session_id), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND workflow_id = sqlc.arg(workflow_id)
  AND task_id = sqlc.arg(task_id)
  AND session_id IS NULL;

-- name: GetWorkflowAttemptSession :one
SELECT session_id FROM workflow_attempts WHERE id = ?;

-- name: InsertWorkflowAttemptRepoBinding :execrows
INSERT INTO workflow_attempt_repo_bindings (
    attempt_id, workflow_id, repo_key, project_id, expected_base_sha,
    session_worktree_ref, observed_head_sha, state, revision, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: ListWorkflowAttemptRepoBindings :many
SELECT attempt_id, workflow_id, repo_key, project_id, expected_base_sha,
    session_worktree_ref, observed_head_sha, state, revision, updated_at
FROM workflow_attempt_repo_bindings
WHERE attempt_id = ?
ORDER BY repo_key;

-- name: UpdateWorkflowAttemptRepoBinding :execrows
-- Per-repository CAS on the observed head and state. A repository whose observed head
-- drifted is reported as mismatch and left in place; M2 never resets or deletes a
-- worktree to "repair" the drift.
UPDATE workflow_attempt_repo_bindings
SET observed_head_sha = sqlc.arg(observed_head_sha),
    session_worktree_ref = sqlc.arg(session_worktree_ref),
    state = sqlc.arg(state),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE attempt_id = sqlc.arg(attempt_id)
  AND repo_key = sqlc.arg(repo_key)
  AND revision = sqlc.arg(expected_revision);

-- name: CountWorkflowAttemptBindingsForSession :one
SELECT COUNT(*) FROM workflow_attempt_bindings WHERE session_id = ?;

-- name: ListWorkflowAttemptBindingsByState :many
-- Startup recovery sweep: every binding whose state means "an external outcome is
-- unproven". The partial index on (workflow_id, state, updated_at) serves the state
-- filter, and updated_at ordering makes a bounded sweep deterministic: the oldest
-- unresolved bindings are reconciled first.
SELECT attempt_id, workflow_id, task_id, spawn_request_id, spawn_hash, state,
    session_id, controller_generation, bound_at, failure_code, revision,
    created_at, updated_at
FROM workflow_attempt_bindings
WHERE state IN (sqlc.slice('states'))
ORDER BY updated_at, attempt_id
-- Unnumbered on purpose: sqlc numbers a named arg (?2) while sqlc.slice expands into
-- positional placeholders, which makes LIMIT bind a state string instead of the limit.
LIMIT ?;
