-- +goose Up
ALTER TABLE conversations ADD COLUMN queue_held_at TIMESTAMP;

-- +goose Down
ALTER TABLE conversations DROP COLUMN queue_held_at;
