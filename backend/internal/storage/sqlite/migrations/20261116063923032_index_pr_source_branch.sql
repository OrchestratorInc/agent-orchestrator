-- Session reads look up the open PR whose source branch a PR targets, to tell
-- a stacked PR from one that can land on its own. Index that lookup so it does
-- not scan every tracked PR on each board read.

-- +goose Up
CREATE INDEX IF NOT EXISTS idx_pr_source_branch ON pr (source_branch, repo);

-- +goose Down
DROP INDEX IF EXISTS idx_pr_source_branch;
