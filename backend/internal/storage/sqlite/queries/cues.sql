-- User-managed reusable quick actions (Cues) scoped to a project. The
-- (project_id, name) UNIQUE constraint enforces per-project name uniqueness;
-- duplicates surface as domain.ErrCueNameExists in the store.

-- name: InsertCue :exec
INSERT INTO cues (
    id, project_id, name, type, command, prompt, created_at, updated_at, run_on_worktree_creation, startup_shell, startup_timeout_seconds
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SelectCueByID :one
SELECT *
FROM cues
WHERE id = ?;

-- name: SelectCuesByProject :many
SELECT *
FROM cues
WHERE project_id = ?
ORDER BY name;

-- name: UpdateCue :one
UPDATE cues
SET name = ?, type = ?, command = ?, prompt = ?, updated_at = ?, run_on_worktree_creation = ?, startup_shell = ?, startup_timeout_seconds = ?
WHERE id = ?
RETURNING *;

-- name: DeleteCueByID :execrows
DELETE FROM cues
WHERE id = ?;

-- name: DisableOtherStartupCues :exec
UPDATE cues SET run_on_worktree_creation = 0 WHERE project_id = ? AND id <> ? AND run_on_worktree_creation = 1;

-- name: SelectStartupCue :one
SELECT * FROM cues WHERE project_id = ? AND run_on_worktree_creation = 1;
