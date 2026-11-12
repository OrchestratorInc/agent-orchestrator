-- +goose Up
ALTER TABLE sessions ADD COLUMN additional_instructions TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN additional_instructions;
