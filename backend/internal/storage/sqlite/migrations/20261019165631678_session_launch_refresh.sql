-- +goose Up
-- Keep accepted work across a failed provider launch refresh and daemon restart.
-- This private recovery intent survives clearing the UI sleep marker on resume.
ALTER TABLE sessions ADD COLUMN launch_refresh_pending INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE sessions DROP COLUMN launch_refresh_pending;
