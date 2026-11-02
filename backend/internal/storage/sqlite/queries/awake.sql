-- name: GetAwakeDocument :one
SELECT document FROM awake_state WHERE id = 1;

-- name: SaveAwakeDocument :exec
UPDATE awake_state SET document = ? WHERE id = 1;

-- name: HasAwakeAgentActivity :one
SELECT CAST(EXISTS(SELECT 1 FROM sessions WHERE is_terminated = 0 AND activity_state = 'active') AS INTEGER);
