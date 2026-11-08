-- +goose Up
ALTER TABLE app_settings ADD COLUMN limit_history INTEGER NOT NULL DEFAULT 0
    CHECK (limit_history IN (0, 1));

-- +goose Down
ALTER TABLE app_settings DROP COLUMN limit_history;
