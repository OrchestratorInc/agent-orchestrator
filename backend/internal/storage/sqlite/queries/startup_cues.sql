-- name: ClaimStartupCue :execrows
UPDATE sessions SET startup_cue_json = ?, updated_at = ?
WHERE id = ? AND startup_cue_json = '' AND is_terminated = 0;

-- name: FinishStartupCue :execrows
UPDATE sessions SET startup_cue_json = ?, updated_at = ?
WHERE id = ? AND (json_extract(NULLIF(startup_cue_json, ''), '$.state') IN ('pending', 'running') OR json_extract(NULLIF(startup_cue_json, ''), '$.deliveryHeld') = 1);

-- name: BeginStartupCue :execrows
UPDATE sessions SET startup_cue_json = ?, updated_at = ?
WHERE id = ? AND is_terminated = 0 AND json_extract(NULLIF(startup_cue_json, ''), '$.state') = 'pending';

-- name: EnqueueStartupCueMessage :execrows
INSERT INTO startup_cue_messages (session_id, client_message_id, message)
SELECT sqlc.arg(session_id), sqlc.arg(client_message_id), sqlc.arg(message)
FROM sessions WHERE id = sqlc.arg(session_id) AND is_terminated = 0
AND (json_extract(NULLIF(startup_cue_json, ''), '$.state') IN ('pending', 'running') OR json_extract(NULLIF(startup_cue_json, ''), '$.deliveryHeld') = 1)
ON CONFLICT(client_message_id) DO NOTHING;

-- name: ListStartupCueMessages :many
SELECT * FROM startup_cue_messages WHERE session_id = ? AND delivered = 0 ORDER BY id;

-- name: MarkStartupCueMessageDelivered :exec
UPDATE startup_cue_messages SET delivered = 1 WHERE id = ?;
