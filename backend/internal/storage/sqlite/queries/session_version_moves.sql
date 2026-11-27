-- name: PutSessionVersionMove :execrows
INSERT INTO session_version_moves (session_id, intent, stage)
VALUES (?, ?, 'stopping')
ON CONFLICT(session_id) DO NOTHING;

-- name: ListSessionVersionMoves :many
SELECT session_id, intent, stage, attempts FROM session_version_moves ORDER BY session_id;

-- name: SetSessionVersionMoveStage :exec
-- A confirmed failed Chat start can claim a generation before publication.
-- Retry fences the next attempt to that stopped epoch without resetting budget.
UPDATE session_version_moves
SET stage = sqlc.arg(stage),
    intent = CASE WHEN sqlc.arg(stage) = 'retry'
        THEN json_set(intent, '$.SourceLaunch',
            (SELECT CASE WHEN session_mode = 'chat' THEN controller_generation ELSE runtime_launch_id END
             FROM sessions WHERE id = sqlc.arg(session_id)))
        ELSE intent END
WHERE session_id = sqlc.arg(session_id);

-- name: AttemptSessionVersionMove :execrows
UPDATE session_version_moves SET attempts = attempts + 1
WHERE session_id = ? AND stage = 'retry' AND attempts < sqlc.arg(max_attempts);

-- name: DeleteSessionVersionMove :exec
DELETE FROM session_version_moves WHERE session_id = ?;
