-- name: UpsertKeptArtifactSession :exec
INSERT INTO kept_artifact_sessions (session_id, project_id, title, kept_at)
VALUES (sqlc.arg(session_id), sqlc.narg(project_id), sqlc.arg(title), sqlc.arg(kept_at))
ON CONFLICT (session_id) DO UPDATE SET
    project_id = excluded.project_id,
    title = excluded.title,
    kept_at = excluded.kept_at;

-- name: ClearKeptArtifacts :exec
DELETE FROM kept_artifacts WHERE session_id = sqlc.arg(session_id);

-- name: CopySessionArtifactsToKept :exec
INSERT INTO kept_artifacts (id, session_id, path, registered_at, previous_paths, created_at)
SELECT a.id, a.session_id, a.path, a.registered_at, a.previous_paths, a.created_at
FROM session_artifacts AS a
WHERE a.session_id = sqlc.arg(session_id);

-- name: ListKeptArtifactSessions :many
-- Only sessions whose row is gone. project_id is cleared for a project that
-- was removed or no longer exists.
SELECT k.session_id, CAST(COALESCE(p.id, '') AS TEXT) AS project_id, k.title, k.kept_at
FROM kept_artifact_sessions AS k
LEFT JOIN projects AS p ON p.id = k.project_id AND p.archived_at IS NULL
WHERE NOT EXISTS (SELECT 1 FROM sessions AS s WHERE s.id = k.session_id)
ORDER BY k.kept_at DESC, k.session_id;

-- name: GetKeptArtifactSession :one
SELECT k.session_id, CAST(COALESCE(p.id, '') AS TEXT) AS project_id, k.title, k.kept_at
FROM kept_artifact_sessions AS k
LEFT JOIN projects AS p ON p.id = k.project_id AND p.archived_at IS NULL
WHERE k.session_id = sqlc.arg(session_id)
  AND NOT EXISTS (SELECT 1 FROM sessions AS s WHERE s.id = k.session_id);

-- name: ListKeptArtifacts :many
SELECT id, session_id, path, registered_at, previous_paths, created_at
FROM kept_artifacts
WHERE session_id = sqlc.arg(session_id)
ORDER BY path;

-- name: DeleteKeptArtifact :exec
DELETE FROM kept_artifacts
WHERE session_id = sqlc.arg(session_id) AND path = sqlc.arg(path);

-- name: DeleteKeptArtifactSession :exec
DELETE FROM kept_artifact_sessions WHERE session_id = sqlc.arg(session_id);

-- name: ListRemovedProjectSessionIDs :many
-- Sessions of a removed project. Their rows stay, but no project lists them.
SELECT s.id
FROM sessions AS s
JOIN projects AS p ON p.id = s.project_id
WHERE p.archived_at IS NOT NULL
ORDER BY s.id;
