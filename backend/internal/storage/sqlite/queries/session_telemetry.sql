-- name: RecordSessionCreatedBy :exec
INSERT INTO session_telemetry (session_id, created_by) VALUES (?, ?)
ON CONFLICT(session_id) DO UPDATE SET created_by = excluded.created_by;

-- name: IncrementSessionWaitingInputCount :exec
INSERT INTO session_telemetry (session_id, waiting_input_count) VALUES (?, 1)
ON CONFLICT(session_id) DO UPDATE SET waiting_input_count = waiting_input_count + 1;

-- name: GetSessionTelemetry :one
SELECT created_by, waiting_input_count FROM session_telemetry WHERE session_id = ?;
