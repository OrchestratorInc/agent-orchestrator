-- +goose Up
-- The PR footer choice a session started with, so a relaunch, a restore or a
-- switch between Chat and the terminal keeps it whatever the settings say by
-- then. NULL: recorded before this column existed, so the current settings
-- decide, as they did before.
ALTER TABLE session_commit_attribution ADD COLUMN pr_footer INTEGER
    CHECK (pr_footer IN (0, 1));

-- +goose Down
ALTER TABLE session_commit_attribution DROP COLUMN pr_footer;
