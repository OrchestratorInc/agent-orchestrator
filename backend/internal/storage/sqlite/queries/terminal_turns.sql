-- name: InsertTerminalTurn :exec
INSERT INTO session_terminal_turns (session_id, harness, native_session_id, user_request, requested_at)
VALUES (sqlc.arg(session_id), sqlc.arg(harness), sqlc.arg(native_session_id), sqlc.arg(user_request), sqlc.arg(requested_at));

-- name: AppendTerminalTurnRequest :execrows
-- Joins a request to the session's newest turn while that turn is still open
-- on the same thread. A side with no thread id matches on the provider alone.
UPDATE session_terminal_turns
SET user_request = session_terminal_turns.user_request || char(10) || char(10) || sqlc.arg(user_request)
WHERE session_terminal_turns.id = (SELECT MAX(t.id) FROM session_terminal_turns AS t WHERE t.session_id = sqlc.arg(session_id))
  AND session_terminal_turns.responded_at IS NULL
  AND session_terminal_turns.harness = sqlc.arg(harness)
  AND (session_terminal_turns.native_session_id IN ('', sqlc.arg(native_session_id)) OR sqlc.arg(native_session_id) = '')
  -- An agent that never reports its stop (Claude Code on an interrupt) would
  -- grow one turn without end; past this size a request starts its own.
  AND length(session_terminal_turns.user_request) < 32000;

-- name: CloseTerminalTurn :exec
-- Answers the session's newest turn. A stop with no open turn changes nothing.
UPDATE session_terminal_turns
SET agent_response = sqlc.arg(agent_response), responded_at = sqlc.arg(responded_at)
WHERE session_terminal_turns.id = (SELECT MAX(t.id) FROM session_terminal_turns AS t WHERE t.session_id = sqlc.arg(session_id))
  AND session_terminal_turns.responded_at IS NULL;

-- name: TrimTerminalTurns :exec
DELETE FROM session_terminal_turns
WHERE session_terminal_turns.session_id = sqlc.arg(session_id) AND session_terminal_turns.id <= (
    SELECT t.id FROM session_terminal_turns AS t WHERE t.session_id = sqlc.arg(session_id)
    ORDER BY t.id DESC LIMIT 1 OFFSET sqlc.arg(keep));

-- name: ListTerminalTurns :many
SELECT harness, user_request, agent_response, requested_at
FROM session_terminal_turns
WHERE session_id = sqlc.arg(session_id)
ORDER BY id;

-- name: DeleteTerminalTurnsForThread :exec
DELETE FROM session_terminal_turns
WHERE session_id = sqlc.arg(session_id)
  AND harness = sqlc.arg(harness)
  AND (native_session_id = sqlc.arg(native_session_id) OR native_session_id = '');
