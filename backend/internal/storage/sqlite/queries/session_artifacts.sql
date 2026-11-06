-- name: InsertSessionArtifact :exec
INSERT INTO session_artifacts (session_id, path, registered_at)
VALUES (?, ?, ?)
ON CONFLICT (session_id, path) DO NOTHING;

-- name: ListSessionArtifactPaths :many
SELECT path FROM session_artifacts
WHERE session_id = sqlc.arg(session_id)
ORDER BY registered_at, path;

-- name: ListSessionArtifactPathsForSessions :many
SELECT session_id, path FROM session_artifacts
WHERE session_id IN (SELECT CAST(j.value AS TEXT) FROM json_each(?) AS j)
ORDER BY session_id, registered_at, path;

-- name: DeleteSessionArtifact :exec
DELETE FROM session_artifacts
WHERE session_id = sqlc.arg(session_id) AND path = sqlc.arg(path);

-- name: DeleteSessionArtifacts :exec
DELETE FROM session_artifacts WHERE session_id = sqlc.arg(session_id);
