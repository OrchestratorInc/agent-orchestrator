-- name: MarkPRMerged :execrows
UPDATE pr
SET pr_state = 'merged',
    is_draft = FALSE,
    is_merged = TRUE,
    is_closed = FALSE,
    merge_commit_sha = ?,
    merge_queue_entry = '',
    state_changed_at = ?,
    updated_at = ?,
    observed_at = ?
WHERE url = ?;

-- name: MarkPRMergeQueue :execrows
UPDATE pr
SET merge_queue_required = TRUE,
    merge_queue_entry = ?,
    updated_at = ?
WHERE url = ? AND pr_state != 'merged';

-- name: MarkPRState :execrows
UPDATE pr
SET pr_state = ?,
    is_draft = ?,
    is_closed = ?,
    state_changed_at = ?,
    updated_at = ?
WHERE url = ? AND pr_state != 'merged';

-- name: SetPRTitle :execrows
UPDATE pr
SET title = ?,
    updated_at = ?
WHERE url = ?;
