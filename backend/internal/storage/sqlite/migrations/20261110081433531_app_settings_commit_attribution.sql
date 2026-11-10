-- +goose Up
-- Off by default: the Orchestrator attribution beta (Settings > Advanced, in
-- Developer mode). When on, new sessions add a Co-authored-by: Orchestrator
-- trailer to their commits and an "Opened with Orchestrator" line to their PRs,
-- unless the project turns those off.
ALTER TABLE app_settings ADD COLUMN commit_attribution INTEGER NOT NULL DEFAULT 0
    CHECK (commit_attribution IN (0, 1));

-- +goose Down
ALTER TABLE app_settings DROP COLUMN commit_attribution;
