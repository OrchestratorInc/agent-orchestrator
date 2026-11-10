-- +goose Up
-- Off by default: the Orchestrator attribution beta (Settings > Advanced, in
-- Developer mode). When on, new sessions add a Co-authored-by: Orchestrator
-- trailer to their commits and an "Opened with Orchestrator" line to their PRs,
-- unless the project turns those off.
ALTER TABLE app_settings ADD COLUMN commit_attribution INTEGER NOT NULL DEFAULT 0
    CHECK (commit_attribution IN (0, 1));

-- The co-author trailer a session started with. A session without a row
-- started without one. When a reclaimed worktree is recreated it gets the same
-- choice back, whatever the settings say by then. Kept apart from sessions,
-- whose every update bumps the revision that fences observer writes.
CREATE TABLE session_commit_attribution (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    co_author  TEXT NOT NULL
);

-- +goose Down
DROP TABLE session_commit_attribution;
ALTER TABLE app_settings DROP COLUMN commit_attribution;
