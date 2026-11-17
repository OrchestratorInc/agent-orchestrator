-- +goose Up
-- Set on a session moved here from AO Cloud when its conversation, or the
-- conversation's sidecar folder, was not placed, so a replayed move/import
-- still tells the app to keep the Cloud session.
ALTER TABLE sessions ADD COLUMN move_conversation_incomplete INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE sessions DROP COLUMN move_conversation_incomplete;
