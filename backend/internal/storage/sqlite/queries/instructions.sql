-- name: ListInstructionRevisions :many
-- An empty project key selects the user's own instructions.
SELECT id, project_id, body, session_id, dreamed, created_at
FROM instruction_revisions
WHERE COALESCE(project_id, '') = sqlc.arg(project_key)
ORDER BY id DESC
LIMIT sqlc.arg(row_limit);

-- name: InsertInstructionRevision :execlastid
INSERT INTO instruction_revisions (project_id, body, session_id, dreamed, created_at)
VALUES (?, ?, ?, ?, ?);

-- name: PruneInstructionRevisions :exec
-- Keeps the newest keep_count revisions plus the newest one dreaming did not
-- write: it decides which lines dreaming must keep.
DELETE FROM instruction_revisions
WHERE COALESCE(instruction_revisions.project_id, '') = sqlc.arg(project_key)
  AND id NOT IN (
      SELECT keep.id FROM instruction_revisions AS keep
      WHERE COALESCE(keep.project_id, '') = sqlc.arg(project_key)
      ORDER BY keep.id DESC
      LIMIT sqlc.arg(keep_count)
  )
  AND id NOT IN (
      SELECT MAX(saved.id) FROM instruction_revisions AS saved
      WHERE COALESCE(saved.project_id, '') = sqlc.arg(project_key) AND saved.dreamed = 0
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
