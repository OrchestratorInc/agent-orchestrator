-- +goose Up
-- The archive retention pass walks conversation_provider_events in id order and
-- records here how far it has gone, so a restart resumes where it stopped instead
-- of reading the archive again. One row, starting before the first archive id.
CREATE TABLE conversation_provider_event_retention (
    id                INTEGER PRIMARY KEY CHECK (id = 1),
    pruned_through_id INTEGER NOT NULL
);
INSERT INTO conversation_provider_event_retention (id, pruned_through_id) VALUES (1, 0);

-- +goose Down
DROP TABLE IF EXISTS conversation_provider_event_retention;
