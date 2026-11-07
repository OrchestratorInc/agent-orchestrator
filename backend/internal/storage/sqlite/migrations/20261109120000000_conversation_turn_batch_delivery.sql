-- +goose Up
-- How a batched queued turn reached the provider: 'drain' when it joined the
-- input its target turn started with, 'steer' when the steer named in
-- batch_delivery_steer_id carried it into a running turn. Turns batched before
-- this column existed stay '' and keep their reports on their own line.
ALTER TABLE conversation_turns ADD COLUMN batch_delivery_kind TEXT NOT NULL DEFAULT ''
    CHECK (batch_delivery_kind IN ('', 'drain', 'steer'));
ALTER TABLE conversation_turns ADD COLUMN batch_delivery_steer_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE conversation_turns DROP COLUMN batch_delivery_steer_id;
ALTER TABLE conversation_turns DROP COLUMN batch_delivery_kind;
