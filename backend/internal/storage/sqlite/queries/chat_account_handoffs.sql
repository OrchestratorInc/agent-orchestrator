-- Automatic usage-limit account handoffs for Chat sessions.

-- name: GetSessionAccountPin :one
SELECT COUNT(*) FROM session_account_pins WHERE session_id = sqlc.arg(session_id);

-- name: PinSessionAccount :exec
INSERT INTO session_account_pins (session_id, pinned_at) VALUES (sqlc.arg(session_id), sqlc.arg(pinned_at))
ON CONFLICT (session_id) DO NOTHING;

-- name: UnpinSessionAccount :exec
DELETE FROM session_account_pins WHERE session_id = sqlc.arg(session_id);

-- Recent failed turns of chat sessions with no retry and no handoff yet. The
-- caller recognizes usage-limit refusals from the error text.
-- name: SelectAccountHandoffCandidateTurns :many
SELECT t.* FROM conversation_turns t
JOIN sessions s ON s.id = t.handled_by_session_id
WHERE t.state = 'failed' AND t.completed_at >= sqlc.arg(since)
  AND t.rolled_back_at IS NULL AND t.handled_by_review_id IS NULL
  AND t.error_message <> ''
  AND s.session_mode = 'chat' AND s.is_terminated = 0
  AND NOT EXISTS (SELECT 1 FROM chat_account_handoffs h WHERE h.source_turn_id = t.id)
  AND NOT EXISTS (SELECT 1 FROM conversation_turns r WHERE r.retry_of_turn_id = t.id)
ORDER BY t.completed_at, t.id;

-- name: InsertChatAccountHandoff :execrows
INSERT INTO chat_account_handoffs (
    source_turn_id, session_id, conversation_id, source_account_id, phase,
    source_resets_at, created_at, updated_at
) VALUES (
    sqlc.arg(source_turn_id), sqlc.arg(session_id), sqlc.arg(conversation_id), sqlc.arg(source_account_id), 'selecting',
    sqlc.narg(source_resets_at), sqlc.arg(created_at), sqlc.arg(created_at)
)
ON CONFLICT (source_turn_id) DO NOTHING;

-- name: GetChatAccountHandoff :one
SELECT * FROM chat_account_handoffs WHERE source_turn_id = sqlc.arg(source_turn_id);

-- name: ListActiveChatAccountHandoffs :many
SELECT * FROM chat_account_handoffs
WHERE phase IN ('selecting', 'moving', 'moved')
ORDER BY created_at, source_turn_id;

-- Compare-and-swap: only the holder of the expected phase advances the saga.
-- name: AdvanceChatAccountHandoff :execrows
UPDATE chat_account_handoffs
SET phase = sqlc.arg(next_phase),
    target_account_id = sqlc.arg(target_account_id),
    transition_id = sqlc.arg(transition_id),
    detail = sqlc.arg(detail),
    updated_at = sqlc.arg(updated_at)
WHERE source_turn_id = sqlc.arg(source_turn_id) AND phase = sqlc.arg(expected_phase);

-- Accounts the session moved away from for a usage limit that has not reset.
-- An unknown reset keeps the account out for the cooldown instead.
-- name: ListRecentlyLeftAccounts :many
SELECT DISTINCT source_account_id FROM chat_account_handoffs
WHERE session_id = sqlc.arg(session_id) AND phase IN ('moving', 'moved', 'retried')
  AND ((source_resets_at IS NOT NULL AND source_resets_at > sqlc.arg(now))
    OR (source_resets_at IS NULL AND created_at > sqlc.arg(cooldown_since)));
