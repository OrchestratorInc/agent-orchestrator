-- name: FindImportedSession :one
SELECT id FROM sessions
WHERE harness = sqlc.arg(harness)
  AND json_extract(NULLIF(import_source, ''), '$.configDir') = CAST(sqlc.arg(config_dir) AS TEXT)
  AND json_extract(NULLIF(import_source, ''), '$.nativeId') = CAST(sqlc.arg(native_id) AS TEXT)
LIMIT 1;

-- name: InsertSessionImportMessage :exec
INSERT INTO session_import_messages (session_id, sequence, role, text, created_at)
VALUES (
    sqlc.arg(session_id), sqlc.arg(sequence), sqlc.arg(role), sqlc.arg(text), sqlc.arg(created_at)
);

-- name: ListSessionImportMessages :many
SELECT sequence, role, text, created_at FROM session_import_messages
WHERE session_id = sqlc.arg(session_id) AND sequence < sqlc.arg(before_sequence)
ORDER BY sequence DESC LIMIT sqlc.arg(page_limit);

-- name: SetSessionImportWorkspace :execrows
UPDATE sessions SET branch = sqlc.arg(branch), workspace_path = sqlc.arg(workspace_path),
    workspace_repo_path = sqlc.arg(workspace_repo_path), import_source = sqlc.arg(new_source),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND is_terminated = 0
    AND import_source = sqlc.arg(expected_source)
    AND json_extract(NULLIF(import_source, ''), '$.adopted') = 0
    AND (workspace_path = '' OR workspace_path = sqlc.arg(workspace_path));
