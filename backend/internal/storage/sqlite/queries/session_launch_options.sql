-- name: GetSessionLaunchOptions :one
SELECT launch_options FROM sessions WHERE id = ?;

-- name: SetSessionLaunchOptions :exec
UPDATE sessions SET launch_options = ? WHERE id = ?;
