-- name: RegisterSemanticDelivery :execrows
INSERT INTO semantic_deliveries (
    session_id, runtime_launch_id, delivery_id
)
SELECT sessions.id, sessions.runtime_launch_id, sqlc.arg(delivery_id)
FROM sessions
WHERE sessions.id = sqlc.arg(session_id)
  AND sessions.runtime_launch_id = sqlc.arg(runtime_launch_id)
  AND sessions.session_mode <> 'chat'
  AND sessions.is_terminated = 0
  AND sessions.activity_state <> 'exited'
ON CONFLICT (session_id, runtime_launch_id, delivery_id) DO UPDATE
SET registered_at = semantic_deliveries.registered_at;

-- name: SemanticDeliveryAccepted :one
SELECT EXISTS (
    SELECT 1
    FROM semantic_deliveries
    JOIN sessions ON sessions.id = semantic_deliveries.session_id
    WHERE sessions.id = sqlc.arg(session_id)
      AND semantic_deliveries.runtime_launch_id = sqlc.arg(runtime_launch_id)
      AND semantic_deliveries.delivery_id = sqlc.arg(delivery_id)
      AND semantic_deliveries.accepted_at IS NOT NULL
);

-- name: AcceptSemanticDelivery :execrows
UPDATE semantic_deliveries
SET accepted_at = CURRENT_TIMESTAMP
WHERE session_id = (
    SELECT sessions.id
    FROM sessions
    WHERE sessions.id = sqlc.arg(session_id)
      AND sessions.runtime_launch_id = sqlc.arg(runtime_launch_id)
      AND sessions.session_mode <> 'chat'
      AND sessions.is_terminated = 0
)
  AND runtime_launch_id = sqlc.arg(runtime_launch_id)
  AND delivery_id = sqlc.arg(delivery_id)
  AND accepted_at IS NULL;
