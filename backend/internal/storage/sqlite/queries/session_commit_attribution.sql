-- name: UpsertSessionCommitAttribution :exec
INSERT INTO session_commit_attribution (session_id, co_author)
VALUES (?, ?)
ON CONFLICT (session_id) DO UPDATE SET co_author = excluded.co_author;

-- name: GetSessionCommitAttribution :one
SELECT co_author FROM session_commit_attribution WHERE session_id = ?;

-- name: UpsertSessionPRFooter :exec
INSERT INTO session_commit_attribution (session_id, co_author, pr_footer)
VALUES (?, '', ?)
ON CONFLICT (session_id) DO UPDATE SET pr_footer = excluded.pr_footer;

-- name: GetSessionPRFooter :one
SELECT pr_footer FROM session_commit_attribution WHERE session_id = ?;

-- name: HasSessionCommitAttribution :one
SELECT EXISTS (SELECT 1 FROM session_commit_attribution WHERE session_id = ?);
