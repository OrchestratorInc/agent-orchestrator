-- Limit window readings, one row per window each time an account's limits are read.

-- name: InsertAgentLimitReading :exec
INSERT INTO agent_limit_readings (
    account_id, provider, email, window_id, label, duration_minutes, used_percent, resets_at_ms, read_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (account_id, window_id, read_at_ms) DO NOTHING;

-- name: DeleteAgentLimitReadingsBefore :exec
DELETE FROM agent_limit_readings WHERE read_at_ms < ?;

-- name: ListAgentLimitReadingsSince :many
SELECT * FROM agent_limit_readings
WHERE read_at_ms >= ?
ORDER BY account_id, window_id, read_at_ms;
