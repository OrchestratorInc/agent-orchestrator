-- +goose Up
-- The AO deliveries a session's runtime launch last accepted, up to 8, space
-- separated. They are kept apart from the conversation checkpoint, which every
-- human prompt rewrites, so a prompt landing after a report cannot erase that
-- report's acceptance.
ALTER TABLE sessions ADD COLUMN accepted_delivery_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN accepted_delivery_launch_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN accepted_delivery_launch_id;
ALTER TABLE sessions DROP COLUMN accepted_delivery_id;
