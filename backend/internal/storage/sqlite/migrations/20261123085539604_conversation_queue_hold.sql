-- +goose Up
ALTER TABLE conversations ADD COLUMN queue_held_turn_id TEXT;

-- +goose Down
ALTER TABLE conversations DROP COLUMN queue_held_turn_id;
