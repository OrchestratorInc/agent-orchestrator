-- name: SessionPRLinkExists :one
SELECT EXISTS (SELECT 1 FROM session_pr_links WHERE session_id = ? AND url = ?);

-- name: CountSessionPRLinks :one
SELECT COUNT(*) FROM session_pr_links WHERE session_id = ?;

-- name: UpsertSessionPRLink :exec
INSERT INTO session_pr_links (
    session_id, url, provider, host, repository, number, relation, title, state, author, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id, url) DO UPDATE SET
    relation = excluded.relation,
    title = excluded.title,
    state = excluded.state,
    author = excluded.author;

-- name: DeleteSessionPRLink :execrows
DELETE FROM session_pr_links WHERE session_id = ? AND url = ?;

-- name: ListSessionPRLinksForSessions :many
SELECT * FROM session_pr_links
WHERE session_id IN (SELECT CAST(j.value AS TEXT) FROM json_each(?) AS j)
ORDER BY session_id, created_at, url;
