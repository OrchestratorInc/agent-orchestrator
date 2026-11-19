-- +goose Up
ALTER TABLE sessions ADD COLUMN requested_output TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN requested_output;
