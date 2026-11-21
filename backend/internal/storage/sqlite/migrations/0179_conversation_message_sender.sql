-- +goose Up
-- Sender identity is durable presentation metadata for automation-origin messages.
-- The immutable session id is the link target; display name is a send-time snapshot.
ALTER TABLE conversation_messages ADD COLUMN sender_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE conversation_messages ADD COLUMN sender_project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE conversation_messages ADD COLUMN sender_display_name TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite cannot drop columns on every supported version. This migration is forward-only.
