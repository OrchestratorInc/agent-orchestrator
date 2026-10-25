-- Read side of the chat search index. The indexer catches up in
-- (updated_at, id) order from its last cursor and re-reads matched messages
-- at query time, so search never serves text the store no longer holds.

-- name: ListConversationMessagesForSearchAfter :many
SELECT id, text, streaming, updated_at
FROM conversation_messages
WHERE updated_at > sqlc.arg(after_updated_at)
   OR (updated_at = sqlc.arg(after_updated_at) AND id > sqlc.arg(after_id))
ORDER BY updated_at, id
LIMIT sqlc.arg(page_size);

-- name: ListConversationMessageIDsAfter :many
SELECT id FROM conversation_messages
WHERE id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(page_size);

-- name: ListConversationMessagesForSearchByIDs :many
SELECT m.id, m.conversation_id, m.text, m.streaming, m.created_at,
       c.session_id, c.current_session_id, c.project_id,
       c.review_id, COALESCE(r.session_id, '') AS review_session_id,
       c.applied_title, c.provider_title
FROM conversation_messages AS m
JOIN conversations AS c ON c.id = m.conversation_id
LEFT JOIN review AS r ON r.id = c.review_id
WHERE m.id IN (sqlc.slice('ids'));

-- name: ListVisibleConversationMessageIDs :many
-- Same visibility rule as SelectConversationMessages (active branch path,
-- discarded turns hidden), restricted to the given ids.
WITH RECURSIVE active_path(branch_id, max_sequence) AS (
    SELECT conversations.active_branch_id, CAST(NULL AS INTEGER)
    FROM conversations
    WHERE conversations.id = sqlc.arg(conversation_id)
    UNION ALL
    SELECT branch.parent_branch_id,
           CASE
               WHEN path.max_sequence IS NULL THEN branch.fork_after_sequence
               WHEN branch.fork_after_sequence < path.max_sequence THEN branch.fork_after_sequence
               ELSE path.max_sequence
           END
    FROM active_path AS path
    JOIN conversation_branches AS branch ON branch.id = path.branch_id
    WHERE branch.parent_branch_id IS NOT NULL
)
SELECT conversation_messages.id FROM conversation_messages
JOIN active_path AS path ON path.branch_id = conversation_messages.branch_id
WHERE conversation_messages.conversation_id = sqlc.arg(conversation_id)
  AND (path.max_sequence IS NULL OR conversation_messages.sequence <= path.max_sequence)
  AND (conversation_messages.turn_id IS NULL OR conversation_messages.turn_id NOT IN (
      SELECT discarded.id FROM conversation_turns AS discarded
      WHERE discarded.conversation_id = sqlc.arg(conversation_id)
        AND (
          discarded.rolled_back_at IS NOT NULL
          OR discarded.promoted_to_turn_id IS NOT NULL
          OR discarded.state = 'cancelled'
        )
  ))
  AND conversation_messages.id IN (sqlc.slice('ids'));
