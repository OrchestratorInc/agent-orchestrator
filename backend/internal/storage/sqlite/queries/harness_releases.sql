-- name: SetManagedHarnessRelease :exec
-- The release new launches of harness start from, written while the update
-- that installed it still holds the harness.
INSERT INTO managed_harness_releases (harness, release, updated_at)
VALUES (sqlc.arg(harness), sqlc.arg(release), sqlc.arg(updated_at))
ON CONFLICT (harness) DO UPDATE SET release = excluded.release, updated_at = excluded.updated_at;

-- name: ListHarnessReleasesInUse :many
-- Releases a live session of harness still runs: its current launch's row.
SELECT DISTINCT r.release FROM session_harness_releases r
JOIN sessions s ON s.id = r.session_id
WHERE r.harness = sqlc.arg(harness) AND s.harness = r.harness
    AND s.is_terminated = 0 AND s.is_task_preparation = 0
    AND s.hibernated_at IS NULL AND s.activity_state <> 'exited'
    AND r.launch = (CASE WHEN s.session_mode = 'chat' THEN s.controller_generation ELSE s.runtime_launch_id END);

-- name: CarryHarnessReleaseToLaunch :exec
-- A live reconnect keeps the old process under a new generation: it still runs
-- the release its previous launch started from.
INSERT OR REPLACE INTO session_harness_releases (session_id, launch, harness, release)
SELECT r.session_id, sqlc.arg(new_launch), r.harness, r.release FROM session_harness_releases r
WHERE r.session_id = sqlc.arg(session_id) AND r.launch = sqlc.arg(old_launch);

-- name: CarryStaleBinaryToLaunch :execrows
-- The reconnected process is the same one an update left on the old release.
UPDATE sessions SET stale_binary_launch = sqlc.arg(new_launch)
WHERE id = sqlc.arg(session_id) AND stale_binary_since IS NOT NULL
    AND stale_binary_launch = sqlc.arg(old_launch);

-- name: PruneSessionHarnessReleases :exec
-- Rows for launches that are no longer any session's current one.
DELETE FROM session_harness_releases
WHERE NOT EXISTS (
    SELECT 1 FROM sessions s
    WHERE s.id = session_harness_releases.session_id
        AND session_harness_releases.launch =
            (CASE WHEN s.session_mode = 'chat' THEN s.controller_generation ELSE s.runtime_launch_id END)
);

-- name: GetChatControllerGeneration :one
-- The generation a live reconnect replaces, read in the transaction that claims
-- its successor.
SELECT controller_generation FROM sessions
WHERE id = sqlc.arg(id) AND session_mode = 'chat';
