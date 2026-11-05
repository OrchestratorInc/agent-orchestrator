-- M2 workflow intervention queries.
--
-- These statements never read or write message text: the table has no text column. The
-- only correlation handles are client_message_id, chat_turn_id, provider_turn_id, and the
-- controller generation.

-- name: GetWorkflowIntervention :one
SELECT id, workflow_id, task_id, attempt_id, mode, state, session_id,
    controller_generation, client_message_id, operation_sha, target_provider_turn_id,
    chat_turn_id, provider_turn_id, reason_code, queue_available, revision,
    created_at, updated_at, settled_at
FROM workflow_interventions
WHERE id = ?;

-- name: GetWorkflowInterventionByClientMessage :one
SELECT id, workflow_id, task_id, attempt_id, mode, state, session_id,
    controller_generation, client_message_id, operation_sha, target_provider_turn_id,
    chat_turn_id, provider_turn_id, reason_code, queue_available, revision,
    created_at, updated_at, settled_at
FROM workflow_interventions
WHERE workflow_id = ? AND client_message_id = ?;

-- name: InsertWorkflowIntervention :execrows
-- The UNIQUE (workflow_id, client_message_id) constraint is the idempotency guarantee. A
-- second insert for the same key either conflicts here or is reported as a replay by the
-- caller's lookup, never as a second side effect.
INSERT INTO workflow_interventions (
    id, workflow_id, task_id, attempt_id, mode, state, session_id,
    controller_generation, client_message_id, operation_sha, target_provider_turn_id,
    chat_turn_id, provider_turn_id, reason_code, queue_available, revision,
    created_at, updated_at, settled_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: UpdateWorkflowInterventionState :execrows
-- Revision CAS. The state must not change underneath the caller, so expected_state is part
-- of the predicate: two reconcilers deciding different outcomes cannot both apply.
UPDATE workflow_interventions
SET state = sqlc.arg(next_state),
    chat_turn_id = sqlc.arg(chat_turn_id),
    provider_turn_id = sqlc.arg(provider_turn_id),
    reason_code = sqlc.arg(reason_code),
    queue_available = sqlc.arg(queue_available),
    settled_at = sqlc.arg(settled_at),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND revision = sqlc.arg(expected_revision)
  AND state = sqlc.arg(expected_state);

-- name: ListWorkflowInterventionsForAttempt :many
SELECT id, workflow_id, task_id, attempt_id, mode, state, session_id,
    controller_generation, client_message_id, operation_sha, target_provider_turn_id,
    chat_turn_id, provider_turn_id, reason_code, queue_available, revision,
    created_at, updated_at, settled_at
FROM workflow_interventions
WHERE workflow_id = sqlc.arg(workflow_id) AND task_id = sqlc.arg(task_id)
ORDER BY created_at, id;

-- name: CountOpenStopInterventions :one
-- A stop that has not settled blocks another stop for the same attempt: issuing a second
-- interrupt while the first is unknown is exactly the blind retry the design forbids.
SELECT COUNT(*) FROM workflow_interventions
WHERE workflow_id = sqlc.arg(workflow_id)
  AND task_id = sqlc.arg(task_id)
  AND mode = 'stop'
  AND state IN ('stop_requested', 'stop_unknown');
