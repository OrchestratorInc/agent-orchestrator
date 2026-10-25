-- +goose Up

-- The search indexer catches up on chat messages in (updated_at, id) order
-- from its last cursor; without an index every catch-up is a full scan of
-- conversation_messages on the only writer connection.
CREATE INDEX idx_conversation_messages_updated
    ON conversation_messages(updated_at, id);

-- +goose Down
DROP INDEX IF EXISTS idx_conversation_messages_updated;
