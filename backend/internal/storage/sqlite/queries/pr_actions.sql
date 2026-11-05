-- name: MarkPRMerged :execrows
UPDATE pr
SET pr_state = 'merged',
    is_draft = FALSE,
    is_merged = TRUE,
    is_closed = FALSE,
    merge_commit_sha = ?,
    state_changed_at = ?,
    updated_at = ?,
    observed_at = ?
WHERE url = ?;
