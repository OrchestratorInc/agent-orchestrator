-- name: UpsertSessionCommitAttribution :exec
INSERT INTO session_commit_attribution (session_id, co_author)
VALUES (?, ?)
ON CONFLICT (session_id) DO UPDATE SET co_author = excluded.co_author;

-- name: GetSessionCommitAttribution :one
SELECT co_author FROM session_commit_attribution WHERE session_id = ?;
