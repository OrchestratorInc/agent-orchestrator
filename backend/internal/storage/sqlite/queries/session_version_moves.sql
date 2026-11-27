-- name: PutSessionVersionMove :execrows
INSERT INTO session_version_moves (session_id, intent, stage)
VALUES (?, ?, 'stopping')
ON CONFLICT(session_id) DO NOTHING;

-- name: ListSessionVersionMoves :many
SELECT session_id, intent, stage, attempts FROM session_version_moves ORDER BY session_id;

-- name: SetSessionVersionMoveStage :exec
UPDATE session_version_moves SET stage = ? WHERE session_id = ?;

-- name: AttemptSessionVersionMove :execrows
UPDATE session_version_moves SET attempts = attempts + 1
WHERE session_id = ? AND stage = 'retry' AND attempts < sqlc.arg(max_attempts);

-- name: DeleteSessionVersionMove :exec
DELETE FROM session_version_moves WHERE session_id = ?;
