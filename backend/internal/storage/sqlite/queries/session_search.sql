-- Session search index. These tables are owned by the search indexer; the
-- sources they describe (sessions, conversations, transcripts) are read-only.

-- name: DeleteSessionSearchDocsForSource :exec
DELETE FROM session_search_docs
WHERE source_key = ?;

-- name: CountSessionSearchDocsForSource :one
SELECT COUNT(*) FROM session_search_docs WHERE source_key = ?;

-- name: UpsertSessionSearchSource :exec
INSERT INTO session_search_sources (
    source_key, kind, session_id, project_id, harness, path, cursor_value,
    file_size, file_mtime, doc_count, state, last_error_code, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source_key) DO UPDATE SET
    kind = excluded.kind,
    session_id = excluded.session_id,
    project_id = excluded.project_id,
    harness = excluded.harness,
    path = excluded.path,
    cursor_value = excluded.cursor_value,
    file_size = excluded.file_size,
    file_mtime = excluded.file_mtime,
    doc_count = excluded.doc_count,
    state = excluded.state,
    last_error_code = excluded.last_error_code,
    updated_at = excluded.updated_at;

-- name: GetSessionSearchSource :one
SELECT * FROM session_search_sources WHERE source_key = ?;

-- name: ListSessionSearchSources :many
SELECT * FROM session_search_sources
ORDER BY updated_at DESC, source_key;

-- name: CountSessionSearchSourcesByState :many
SELECT state, COUNT(*) AS count FROM session_search_sources GROUP BY state;