-- +goose Up
-- Set on a session moved here from AO Cloud once its conversation and the
-- conversation's sidecar folder are placed. Until then a replayed
-- move/import reports the conversation incomplete, so the app keeps the
-- Cloud session holding the full copy.
ALTER TABLE sessions ADD COLUMN move_conversation_placed INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE sessions DROP COLUMN move_conversation_placed;
