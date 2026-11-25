-- name: RecordPendingTransfer :exec
INSERT INTO session_transfers (session_id, transfer_id, dest_host_id, dest_label, dest_project_id, dest_session_id, created_at)
VALUES (?, ?, ?, ?, ?, '', ?)
ON CONFLICT(session_id) DO UPDATE SET
    transfer_id = excluded.transfer_id,
    dest_host_id = excluded.dest_host_id,
    dest_label = excluded.dest_label,
    dest_project_id = excluded.dest_project_id,
    created_at = excluded.created_at
WHERE session_transfers.dest_session_id = '';

-- name: FinishPendingTransfer :execrows
UPDATE session_transfers SET dest_label = ?, dest_session_id = ?, created_at = ?
WHERE session_id = ? AND transfer_id = ? AND dest_host_id = ? AND dest_project_id = ? AND dest_session_id = '';

-- name: CancelPendingTransfer :execrows
DELETE FROM session_transfers WHERE session_id = ? AND transfer_id = ? AND dest_session_id = '';

-- name: GetSessionTransfer :one
SELECT transfer_id, dest_host_id, dest_label, dest_project_id, dest_session_id FROM session_transfers WHERE session_id = ?;

-- name: ListTransferredSessionIDs :many
SELECT session_id FROM session_transfers WHERE dest_session_id != '';
