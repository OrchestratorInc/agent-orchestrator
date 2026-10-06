-- +goose Up
ALTER TABLE ao_sessions ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE ao_sessions DROP COLUMN reasoning_effort;
