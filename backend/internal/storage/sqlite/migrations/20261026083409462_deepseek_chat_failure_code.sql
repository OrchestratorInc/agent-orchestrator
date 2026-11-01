-- +goose Up
ALTER TABLE conversation_turns ADD COLUMN failure_code TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE conversation_turns DROP COLUMN failure_code;
