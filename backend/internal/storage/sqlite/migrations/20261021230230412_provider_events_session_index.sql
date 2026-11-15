-- +goose Up

-- Session deletes cascade here by session_id; without an index that is a
-- full scan of the provider archive on the only writer connection.
CREATE INDEX idx_conversation_provider_events_session
    ON conversation_provider_events(session_id);

-- +goose Down
DROP INDEX IF EXISTS idx_conversation_provider_events_session;
