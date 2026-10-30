-- name: RecordSessionInteraction :execrows
UPDATE sessions SET
    latest_interaction_at = sqlc.arg(interaction_at),
    updated_at = MAX(updated_at, sqlc.arg(interaction_at))
WHERE id = sqlc.arg(id)
  AND is_terminated = 0
  AND (latest_interaction_at IS NULL OR latest_interaction_at < sqlc.arg(interaction_at));
