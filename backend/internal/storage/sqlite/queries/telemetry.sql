-- name: CreateTelemetryEvent :exec
INSERT INTO telemetry_event (
    id, occurred_at, name, source, level, project_id, session_id, request_id, payload_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListTelemetryEventsSince :many
SELECT id, occurred_at, name, source, level, project_id, session_id, request_id, payload_json
FROM telemetry_event
WHERE occurred_at >= ? COLLATE ao_timestamp
ORDER BY occurred_at COLLATE ao_timestamp ASC
LIMIT ?;

-- name: PruneTelemetryEventsBefore :execrows
DELETE FROM telemetry_event
WHERE id IN (
    SELECT te.id
    FROM telemetry_event te
    WHERE te.occurred_at < ? COLLATE ao_timestamp
    ORDER BY te.occurred_at COLLATE ao_timestamp ASC
    LIMIT ?
);
