-- +goose Up
ALTER TABLE cues DROP COLUMN startup_shell;
ALTER TABLE cues DROP COLUMN startup_timeout_seconds;

-- +goose Down
ALTER TABLE cues ADD COLUMN startup_shell TEXT NOT NULL DEFAULT '';
ALTER TABLE cues ADD COLUMN startup_timeout_seconds INTEGER NOT NULL DEFAULT 600;
