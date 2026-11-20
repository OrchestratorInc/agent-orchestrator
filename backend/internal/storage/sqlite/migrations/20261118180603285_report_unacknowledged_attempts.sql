-- +goose Up
ALTER TABLE reports ADD COLUMN unacknowledged_attempts INTEGER NOT NULL DEFAULT 0 CHECK (unacknowledged_attempts >= 0);

-- +goose Down
ALTER TABLE reports DROP COLUMN unacknowledged_attempts;
