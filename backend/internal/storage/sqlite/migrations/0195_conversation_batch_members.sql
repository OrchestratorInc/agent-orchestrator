-- +goose Up
ALTER TABLE conversation_turns ADD COLUMN batched_input INTEGER NOT NULL DEFAULT 0;
ALTER TABLE conversation_turns ADD COLUMN provider_input_text TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE conversation_turns DROP COLUMN provider_input_text;
ALTER TABLE conversation_turns DROP COLUMN batched_input;
