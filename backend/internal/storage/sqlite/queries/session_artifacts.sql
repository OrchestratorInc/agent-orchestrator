-- name: ListSessionArtifacts :many
-- has_versions tells a scan which rows have kept versions: a file moved onto
-- such a row's path joins that artifact instead of replacing it.
SELECT sqlc.embed(a),
    CAST(EXISTS (
        SELECT 1 FROM session_artifact_versions AS v
        WHERE v.session_id = a.session_id AND v.path = a.path
    ) AS INTEGER) AS has_versions
FROM session_artifacts AS a
WHERE a.session_id = sqlc.arg(session_id)
ORDER BY a.path;

-- name: ListSessionArtifactsForSessions :many
SELECT sqlc.embed(a),
    CAST(EXISTS (
        SELECT 1 FROM session_artifact_versions AS v
        WHERE v.session_id = a.session_id AND v.path = a.path
    ) AS INTEGER) AS has_versions
FROM session_artifacts AS a
WHERE a.session_id IN (SELECT CAST(j.value AS TEXT) FROM json_each(?) AS j)
ORDER BY a.session_id, a.path;

-- name: InsertSessionArtifact :exec
INSERT INTO session_artifacts (
    id, session_id, path, registered_at, file_dev, file_ino, file_birth, file_generation, file_mtime, previous_paths, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', ?)
ON CONFLICT (session_id, path) DO NOTHING;

-- name: RegisterSessionArtifact :exec
UPDATE session_artifacts
SET registered_at = COALESCE(registered_at, sqlc.arg(registered_at)),
    file_dev = sqlc.arg(file_dev), file_ino = sqlc.arg(file_ino), file_birth = sqlc.arg(file_birth),
    file_generation = sqlc.arg(file_generation), file_mtime = sqlc.arg(file_mtime)
WHERE session_id = sqlc.arg(session_id) AND path = sqlc.arg(path);

-- name: ListRegisteredSessionArtifactPaths :many
SELECT path FROM session_artifacts
WHERE session_id = sqlc.arg(session_id) AND registered_at IS NOT NULL
ORDER BY registered_at, path;

-- name: SetSessionArtifactFile :execrows
UPDATE session_artifacts
SET file_dev = sqlc.arg(file_dev), file_ino = sqlc.arg(file_ino), file_birth = sqlc.arg(file_birth),
    file_generation = sqlc.arg(file_generation), file_mtime = sqlc.arg(file_mtime)
WHERE id = sqlc.arg(id) AND path = sqlc.arg(path);

-- name: MoveSessionArtifact :execrows
UPDATE session_artifacts
SET path = sqlc.arg(to_path), previous_paths = sqlc.arg(previous_paths),
    file_dev = sqlc.arg(file_dev), file_ino = sqlc.arg(file_ino), file_birth = sqlc.arg(file_birth),
    file_generation = sqlc.arg(file_generation), file_mtime = sqlc.arg(file_mtime)
WHERE id = sqlc.arg(id) AND path = sqlc.arg(from_path);

-- name: MoveSessionArtifactVersions :exec
UPDATE session_artifact_versions
SET path = sqlc.arg(to_path)
WHERE session_id = sqlc.arg(session_id) AND path = sqlc.arg(from_path);

-- name: DeleteSessionArtifactByID :execrows
DELETE FROM session_artifacts
WHERE id = sqlc.arg(id) AND path = sqlc.arg(path);

-- name: ForgetSessionArtifact :execrows
DELETE FROM session_artifacts
WHERE id = (
    SELECT a.id FROM session_artifacts AS a
    WHERE a.id = sqlc.arg(id) AND a.path = sqlc.arg(path) AND a.registered_at IS NULL
      AND NOT EXISTS (
          SELECT 1 FROM session_artifact_versions AS v
          WHERE v.session_id = a.session_id AND v.path = a.path
      )
);

-- name: DeleteSessionArtifact :exec
DELETE FROM session_artifacts
WHERE session_id = sqlc.arg(session_id) AND path = sqlc.arg(path);

-- name: DeleteSessionArtifacts :exec
DELETE FROM session_artifacts WHERE session_id = sqlc.arg(session_id);
