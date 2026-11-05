-- M2-06 collaboration queries.
--
-- None of these statements reads or writes message body into a change_log payload: the
-- triggers carry ids/kind/state only. These statements do read body/payload_ref for the
-- caller that is authorized to see them.

-- name: InsertWorkflowMessage :execrows
-- The UNIQUE (workflow_id, client_message_id) constraint is the idempotency guarantee: a
-- replay either conflicts here or is reported by the caller's lookup, so a retried message is
-- never stored twice.
INSERT INTO workflow_messages (
    id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetWorkflowMessage :one
SELECT id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
FROM workflow_messages
WHERE workflow_id = sqlc.arg(workflow_id) AND id = sqlc.arg(id);

-- name: GetWorkflowMessageByClientMessage :one
SELECT id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
FROM workflow_messages
WHERE workflow_id = sqlc.arg(workflow_id) AND client_message_id = sqlc.arg(client_message_id);

-- name: ListWorkflowMessages :many
SELECT id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
FROM workflow_messages
WHERE workflow_id = sqlc.arg(workflow_id)
ORDER BY created_at, id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountOpenWorkflowMessages :one
-- Queue depth is a store check, not a schema constraint: SQLite cannot express a row-count
-- CHECK. This statement is evaluated inside the insert transaction so two concurrent inserts
-- cannot both pass the cap.
SELECT COUNT(*) FROM workflow_messages
WHERE workflow_id = sqlc.arg(workflow_id)
  AND delivery_state IN ('persisted', 'unknown');

-- name: UpdateWorkflowMessageDelivery :execrows
UPDATE workflow_messages
SET delivery_state = sqlc.arg(delivery_state),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE workflow_id = sqlc.arg(workflow_id)
  AND id = sqlc.arg(id)
  AND revision = sqlc.arg(expected_revision);

-- name: InsertWorkflowTaskRequest :execrows
INSERT INTO workflow_task_requests (
    id, workflow_id, requester_task_id, request_kind, request_json, request_sha,
    state, decided_by_kind, decided_by_ref, decided_at, reason, workflow_revision,
    revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, '', NULL, '', ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetWorkflowTaskRequest :one
SELECT id, workflow_id, requester_task_id, request_kind, request_json, request_sha,
    state, decided_by_kind, decided_by_ref, decided_at, reason, workflow_revision,
    revision, created_at, updated_at
FROM workflow_task_requests
WHERE workflow_id = sqlc.arg(workflow_id) AND id = sqlc.arg(id);

-- name: ListWorkflowTaskRequests :many
SELECT id, workflow_id, requester_task_id, request_kind, request_json, request_sha,
    state, decided_by_kind, decided_by_ref, decided_at, reason, workflow_revision,
    revision, created_at, updated_at
FROM workflow_task_requests
WHERE workflow_id = sqlc.arg(workflow_id)
ORDER BY created_at, id
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: DecideWorkflowTaskRequest :execrows
-- Revision CAS plus a state CAS: two deciders cannot both apply, and a decided row cannot be
-- re-decided.
UPDATE workflow_task_requests
SET state = sqlc.arg(next_state),
    decided_by_kind = sqlc.arg(decided_by_kind),
    decided_by_ref = sqlc.arg(decided_by_ref),
    decided_at = sqlc.arg(decided_at),
    reason = sqlc.arg(reason),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE workflow_id = sqlc.arg(workflow_id)
  AND id = sqlc.arg(id)
  AND revision = sqlc.arg(expected_revision)
  AND state = 'pending';

-- name: CountPendingWorkflowTaskRequests :one
SELECT COUNT(*) FROM workflow_task_requests
WHERE workflow_id = sqlc.arg(workflow_id) AND requester_task_id = sqlc.arg(requester_task_id)
  AND state = 'pending';
