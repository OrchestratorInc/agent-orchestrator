-- name: EnqueueWorkerTurnFailure :exec
INSERT OR IGNORE INTO worker_turn_failure_delivery (
    turn_id, session_id, project_id, created_at, next_attempt_at
)
SELECT t.id, s.id, s.project_id, sqlc.arg(created_at), sqlc.arg(created_at)
FROM conversation_turns AS t
JOIN sessions AS s ON s.id = t.handled_by_session_id
WHERE t.conversation_id = sqlc.arg(conversation_id)
  AND t.provider_turn_id = sqlc.arg(provider_turn_id)
  AND t.state = 'failed'
  AND t.handled_by_review_id IS NULL
  AND s.kind = 'worker';

-- name: ListDueWorkerTurnFailures :many
SELECT d.turn_id, d.session_id, d.project_id, d.attempts,
       d.target_session_id, t.error_message, s.display_name
FROM worker_turn_failure_delivery AS d
JOIN conversation_turns AS t ON t.id = d.turn_id
JOIN sessions AS s ON s.id = d.session_id
WHERE d.accepted_at IS NULL AND d.next_attempt_at <= sqlc.arg(now)
ORDER BY d.next_attempt_at, d.created_at, d.turn_id
LIMIT sqlc.arg(limit);

-- name: AcknowledgeWorkerTurnFailure :execrows
UPDATE worker_turn_failure_delivery
SET accepted_at = sqlc.arg(accepted_at), last_error = ''
WHERE turn_id = sqlc.arg(turn_id) AND accepted_at IS NULL;

-- name: BindWorkerTurnFailureTarget :execrows
UPDATE worker_turn_failure_delivery
SET target_session_id = sqlc.arg(target_session_id)
WHERE turn_id = sqlc.arg(turn_id)
  AND target_session_id IS NULL AND accepted_at IS NULL;

-- name: RetryWorkerTurnFailure :execrows
UPDATE worker_turn_failure_delivery
SET attempts = attempts + 1, next_attempt_at = sqlc.arg(next_attempt_at),
    last_error = sqlc.arg(last_error)
WHERE turn_id = sqlc.arg(turn_id) AND accepted_at IS NULL;
