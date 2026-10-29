-- name: UpsertMCPApp :exec
INSERT INTO conversation_mcp_apps(activity_id, data_json)
SELECT id, sqlc.arg(data_json) FROM conversation_activities
WHERE conversation_id = sqlc.arg(conversation_id) AND provider_item_id = sqlc.arg(provider_item_id)
ON CONFLICT(activity_id) DO UPDATE SET data_json = excluded.data_json;

-- name: SelectMCPApp :one
WITH RECURSIVE active_path(branch_id, max_sequence) AS (
    SELECT active_branch_id, CAST(NULL AS INTEGER) FROM conversations WHERE id = sqlc.arg(conversation_id)
    UNION ALL
    SELECT branch.parent_branch_id,
        CASE WHEN path.max_sequence IS NULL THEN branch.fork_after_sequence
             WHEN branch.fork_after_sequence < path.max_sequence THEN branch.fork_after_sequence
             ELSE path.max_sequence END
    FROM active_path AS path JOIN conversation_branches AS branch ON branch.id = path.branch_id
    WHERE branch.parent_branch_id IS NOT NULL
)
SELECT conversation_mcp_apps.*, conversation_activities.conversation_id
FROM conversation_mcp_apps JOIN conversation_activities ON conversation_activities.id = activity_id
JOIN active_path AS path ON path.branch_id = conversation_activities.branch_id
WHERE activity_id = sqlc.arg(activity_id) AND conversation_activities.conversation_id = sqlc.arg(conversation_id)
AND (path.max_sequence IS NULL OR conversation_activities.sequence <= path.max_sequence)
AND (conversation_activities.turn_id IS NULL OR conversation_activities.turn_id NOT IN (
    SELECT id FROM conversation_turns WHERE conversation_id = sqlc.arg(conversation_id)
    AND (rolled_back_at IS NOT NULL OR state = 'cancelled')
));

-- name: SetMCPAppDocument :exec
UPDATE conversation_mcp_apps SET document_json = ? WHERE activity_id = ? AND document_json = '';

-- name: SetMCPAppContext :exec
UPDATE conversation_mcp_apps SET context_text = ? WHERE activity_id = ?;

-- name: SetMCPAppDocumentForItem :exec
UPDATE conversation_mcp_apps SET document_json = sqlc.arg(document_json)
WHERE document_json = '' AND activity_id IN (
    SELECT id FROM conversation_activities WHERE conversation_id = sqlc.arg(conversation_id) AND provider_item_id = sqlc.arg(provider_item_id)
);

-- Active branch membership and rollback use the same rules as the transcript.
-- name: SelectVisibleMCPApps :many
WITH RECURSIVE active_path(branch_id, max_sequence) AS (
    SELECT active_branch_id, CAST(NULL AS INTEGER) FROM conversations WHERE id = sqlc.arg(conversation_id)
    UNION ALL
    SELECT branch.parent_branch_id,
        CASE WHEN path.max_sequence IS NULL THEN branch.fork_after_sequence
             WHEN branch.fork_after_sequence < path.max_sequence THEN branch.fork_after_sequence
             ELSE path.max_sequence END
    FROM active_path AS path JOIN conversation_branches AS branch ON branch.id = path.branch_id
    WHERE branch.parent_branch_id IS NOT NULL
)
SELECT activity_id, context_text, conversation_activities.conversation_id,
    CAST(json_object('server', json_extract(data_json, '$.server'), 'tool', json_extract(data_json, '$.tool'),
        'rootThreadId', json_extract(data_json, '$.rootThreadId')) AS TEXT) AS data_json
FROM conversation_mcp_apps
JOIN conversation_activities ON conversation_activities.id = activity_id
JOIN active_path AS path ON path.branch_id = conversation_activities.branch_id
WHERE conversation_activities.conversation_id = sqlc.arg(conversation_id)
AND context_text != ''
AND (path.max_sequence IS NULL OR conversation_activities.sequence <= path.max_sequence)
AND (conversation_activities.turn_id IS NULL OR conversation_activities.turn_id NOT IN (
    SELECT id FROM conversation_turns WHERE conversation_id = sqlc.arg(conversation_id)
    AND (rolled_back_at IS NOT NULL OR state = 'cancelled')
))
ORDER BY conversation_activities.sequence DESC LIMIT 32;
