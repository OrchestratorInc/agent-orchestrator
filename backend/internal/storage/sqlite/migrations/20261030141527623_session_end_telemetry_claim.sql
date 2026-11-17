-- +goose Up
ALTER TABLE session_telemetry ADD COLUMN end_claimed INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE session_telemetry DROP COLUMN end_claimed;
