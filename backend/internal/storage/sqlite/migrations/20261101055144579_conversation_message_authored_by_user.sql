-- +goose Up
-- User-written content that AO delivered as automation, such as Browser-panel
-- annotations. The origin stays automation; this durable fact lets the user
-- steer or cancel their own queued turn.
ALTER TABLE conversation_messages ADD COLUMN authored_by_user INTEGER NOT NULL DEFAULT 0 CHECK (authored_by_user IN (0, 1));

-- +goose Down
ALTER TABLE conversation_messages DROP COLUMN authored_by_user;
