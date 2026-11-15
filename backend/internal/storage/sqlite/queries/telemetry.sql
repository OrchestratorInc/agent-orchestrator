-- name: CreateTelemetryEvent :exec
INSERT INTO telemetry_event (
    id, occurred_at, name, source, level, project_id, session_id, request_id, payload_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListTelemetryEventsSince :many
SELECT id, occurred_at, name, source, level, project_id, session_id, request_id, payload_json
FROM telemetry_event
WHERE occurred_at >= ?
ORDER BY occurred_at ASC
LIMIT ?;

-- name: PruneTelemetryEventsBefore :execrows
DELETE FROM telemetry_event
WHERE id IN (
    SELECT te.id
    FROM telemetry_event te
    WHERE te.occurred_at < ?
    ORDER BY te.occurred_at ASC
    LIMIT ?
);
