-- name: UpsertSessionCleanupFacts :exec
INSERT INTO session_cleanup_facts (
    session_id, session_generation, runtime_released_at, workspace_disposition,
    attempt_count, last_attempt_at, next_attempt_at, failure_code
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id) DO UPDATE SET
    session_generation = excluded.session_generation,
    runtime_released_at = excluded.runtime_released_at,
    workspace_disposition = excluded.workspace_disposition,
    attempt_count = excluded.attempt_count,
    last_attempt_at = excluded.last_attempt_at,
    next_attempt_at = excluded.next_attempt_at,
    failure_code = excluded.failure_code;

-- name: GetSessionCleanupFacts :one
SELECT session_id, session_generation, runtime_released_at, workspace_disposition,
    attempt_count, last_attempt_at, next_attempt_at, failure_code
FROM session_cleanup_facts
WHERE session_id = ?;

-- name: ListSessionCleanupFactsBySessions :many
-- Batch form of GetSessionCleanupFacts for session-list reads: the JSON array of
-- session ids replaces one lookup per archived session. Sessions with no facts
-- row are simply absent from the result.
WITH wanted_session AS (
    SELECT CAST(j.value AS TEXT) AS session_id
    FROM json_each(?) AS j
)
SELECT f.session_id, f.session_generation, f.runtime_released_at, f.workspace_disposition,
    f.attempt_count, f.last_attempt_at, f.next_attempt_at, f.failure_code
FROM session_cleanup_facts f
JOIN wanted_session ON wanted_session.session_id = f.session_id;

-- NOTE: the coarse sessions-driven candidate scan (ListTerminalCleanupCandidates)
-- is intentionally NOT a sqlc query. sqlc 1.31's SQLite parser strips the trailing
-- clause after the final `?` placeholder (the same bug documented in
-- queries/changelog.sql), and the scan's parameter sits inside a nested WHERE
-- group whose closing parens then get truncated into invalid SQL. The store runs
-- it directly via readDB.QueryContext (see store/session_cleanup_store.go).
