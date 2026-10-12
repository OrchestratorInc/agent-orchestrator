-- name: ListLatestSessionArtifactVersions :many
SELECT v.* FROM session_artifact_versions AS v
WHERE v.session_id = sqlc.arg(session_id)
  AND v.version = (
      SELECT MAX(m.version) FROM session_artifact_versions AS m
      WHERE m.session_id = v.session_id AND m.path = v.path
  )
ORDER BY v.path;

-- name: ListSessionArtifactVersions :many
SELECT * FROM session_artifact_versions
WHERE session_id = sqlc.arg(session_id) AND path = sqlc.arg(path)
ORDER BY version DESC;

-- name: NextSessionArtifactVersion :one
SELECT CAST(COALESCE(MAX(version), 0) + 1 AS INTEGER) AS next_version
FROM session_artifact_versions
WHERE session_id = sqlc.arg(session_id) AND path = sqlc.arg(path);

-- name: InsertSessionArtifactVersion :one
INSERT INTO session_artifact_versions (
    session_id, path, version, sha256, size, content_type, mtime, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DeleteSessionArtifactVersionsBelow :many
DELETE FROM session_artifact_versions
WHERE session_id = sqlc.arg(session_id) AND path = sqlc.arg(path)
  AND version < sqlc.arg(min_version)
RETURNING sha256;

-- name: CountSessionArtifactVersionsBySHA :one
SELECT COUNT(*) FROM session_artifact_versions
WHERE session_id = sqlc.arg(session_id) AND sha256 = sqlc.arg(sha256);
