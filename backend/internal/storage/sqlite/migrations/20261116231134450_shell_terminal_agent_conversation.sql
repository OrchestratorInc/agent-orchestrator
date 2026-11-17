-- +goose Up
-- The conversation of the coding agent that was running in a user shell when
-- the app quit, so the restored tab can resume exactly that conversation.
-- Empty when no agent was running or its conversation is not known.
ALTER TABLE shell_terminals ADD COLUMN agent_conversation_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE shell_terminals DROP COLUMN agent_conversation_id;
