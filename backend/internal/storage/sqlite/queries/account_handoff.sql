-- name: GetSessionAccountPin :one
SELECT COUNT(*) FROM session_account_pins WHERE session_id = sqlc.arg(session_id);

-- name: PinSessionAccount :exec
INSERT INTO session_account_pins (session_id) VALUES (sqlc.arg(session_id))
ON CONFLICT (session_id) DO NOTHING;

-- name: UnpinSessionAccount :exec
DELETE FROM session_account_pins WHERE session_id = sqlc.arg(session_id);

-- name: SelectAccountHandoffCandidateTurns :many
SELECT t.* FROM conversation_turns t
JOIN sessions s ON s.id = t.handled_by_session_id
WHERE t.state = 'failed' AND t.completed_at >= sqlc.arg(since)
  AND t.rolled_back_at IS NULL AND t.handled_by_review_id IS NULL
  AND t.error_message <> ''
  AND s.session_mode = 'chat' AND s.is_terminated = 0
  AND NOT EXISTS (SELECT 1 FROM conversation_turns r WHERE r.retry_of_turn_id = t.id)
ORDER BY t.completed_at, t.id;
