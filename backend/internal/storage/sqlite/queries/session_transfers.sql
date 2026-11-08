-- name: RecordSessionTransfer :exec
INSERT INTO session_transfers (session_id, transfer_id, dest_host_id, dest_label, dest_session_id, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO NOTHING;

-- name: GetSessionTransfer :one
SELECT transfer_id, dest_host_id, dest_label, dest_session_id FROM session_transfers WHERE session_id = ?;

-- name: ListTransferredSessionIDs :many
SELECT session_id FROM session_transfers;
