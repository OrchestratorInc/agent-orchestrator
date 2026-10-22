-- +goose Up
-- A continuation is the user's one-click "continue" after stopping a turn. The
-- agent receives its text like any message; the chat shows a quiet marker in
-- place of a message bubble. Recorded as a durable fact, never inferred from
-- the message text.
ALTER TABLE conversation_messages ADD COLUMN continuation INTEGER NOT NULL DEFAULT 0 CHECK (continuation IN (0, 1));

-- +goose Down
ALTER TABLE conversation_messages DROP COLUMN continuation;
