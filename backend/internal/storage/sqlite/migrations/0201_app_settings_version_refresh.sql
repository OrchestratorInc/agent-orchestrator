-- +goose Up
-- On by default: after a harness update, idle open sessions move to the new
-- release instead of running the old one until they happen to restart.
ALTER TABLE app_settings ADD COLUMN version_refresh INTEGER NOT NULL DEFAULT 1
    CHECK (version_refresh IN (0, 1));

-- +goose Down
ALTER TABLE app_settings DROP COLUMN version_refresh;
