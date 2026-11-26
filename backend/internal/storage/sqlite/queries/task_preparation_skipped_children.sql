-- name: RecordTaskPreparationSkippedChild :exec
INSERT INTO task_preparation_skipped_children (session_id, repo_name, worktree_path)
SELECT id, sqlc.arg(repo_name), sqlc.arg(worktree_path) FROM sessions
WHERE id = sqlc.arg(session_id) AND is_task_preparation = 1;

-- name: ListTaskPreparationSkippedChildren :many
SELECT repo_name, worktree_path FROM task_preparation_skipped_children
WHERE session_id = ?;
