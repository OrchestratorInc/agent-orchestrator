-- name: UpsertSessionTerminalTitle :exec
INSERT INTO session_terminal_titles (session_id, title, updated_at)
VALUES (?, ?, ?)
ON CONFLICT (session_id) DO UPDATE
SET title = excluded.title, updated_at = excluded.updated_at
WHERE session_terminal_titles.title <> excluded.title;

-- name: ListSessionTerminalTitles :many
SELECT session_id, title
FROM session_terminal_titles
WHERE session_id IN (SELECT CAST(j.value AS TEXT) FROM json_each(?) AS j);
