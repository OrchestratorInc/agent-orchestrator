-- name: ListInstructionRevisions :many
-- An empty project key selects the user's own instructions.
SELECT id, project_id, body, session_id, dreamed, created_at, user_lines
FROM instruction_revisions
WHERE COALESCE(project_id, '') = sqlc.arg(project_key)
ORDER BY id DESC
LIMIT sqlc.arg(row_limit);

-- name: InsertInstructionRevision :execlastid
INSERT INTO instruction_revisions (project_id, body, session_id, dreamed, created_at, user_lines)
VALUES (?, ?, ?, ?, ?, ?);

-- name: PruneInstructionRevisions :exec
-- Keeps the newest keep_count revisions. Each revision carries the user's
-- lines itself, so older ones are only history.
DELETE FROM instruction_revisions
WHERE COALESCE(instruction_revisions.project_id, '') = sqlc.arg(project_key)
  AND id NOT IN (
      SELECT keep.id FROM instruction_revisions AS keep
      WHERE COALESCE(keep.project_id, '') = sqlc.arg(project_key)
      ORDER BY keep.id DESC
      LIMIT sqlc.arg(keep_count)
  );

-- name: ListHumanMessagesForSession :many
-- What the user typed in a session's own conversations, oldest first.
SELECT m.text
FROM conversation_messages AS m
JOIN conversations AS c ON c.id = m.conversation_id
WHERE c.session_id = ? AND m.role = 'user' AND m.origin = 'human' AND m.text <> ''
ORDER BY m.created_at, m.sequence;

-- name: CountSessionConversations :one
SELECT COUNT(*) FROM conversations WHERE session_id = ?;

-- name: ListSessionTurnStates :many
-- The states of a session's own conversation turns, oldest first.
SELECT t.state
FROM conversation_turns AS t
JOIN conversations AS c ON c.id = t.conversation_id
WHERE c.session_id = ?
ORDER BY t.requested_at, t.id;

-- name: ListSessionCommands :many
-- The newest commands the agent ran in a session's own conversations that
-- passed or failed, newest first.
SELECT a.summary, a.status
FROM conversation_activities AS a
JOIN conversations AS c ON c.id = a.conversation_id
WHERE c.session_id = sqlc.arg(session_id) AND a.kind = 'command'
  AND a.status IN ('completed', 'failed') AND a.summary <> ''
ORDER BY a.created_at DESC, a.sequence DESC
LIMIT sqlc.arg(row_limit);
