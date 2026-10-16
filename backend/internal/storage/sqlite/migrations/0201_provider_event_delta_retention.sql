-- +goose Up
-- The archive retention pass deletes streamed message and reasoning deltas that
-- carry no provider event id once their turn has been settled for the retention
-- window. Indexing only those rows, by arrival time, lets each batch find the
-- oldest ones without reading the rest of the archive; an entry leaves the index
-- when its row is pruned, so the index stays the size of the window.
-- Building it scans the whole archive once, at the first start after upgrade.
CREATE INDEX idx_conversation_provider_events_anonymous_deltas
    ON conversation_provider_events(received_at)
    WHERE provider_event_id = '' AND method IN ('message.delta', 'reasoning.delta');

-- +goose Down
DROP INDEX IF EXISTS idx_conversation_provider_events_anonymous_deltas;
