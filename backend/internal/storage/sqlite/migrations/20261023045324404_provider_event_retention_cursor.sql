-- +goose Up
-- The archive retention pass walks conversation_provider_events in id order and
-- records here how far it has gone, so a restart resumes where it stopped instead
-- of reading the archive again. One row, starting before the first archive id.
-- IF NOT EXISTS / OR IGNORE: a database that ran this migration under its earlier
-- number (0201) keeps its table and cursor instead of failing to start.
CREATE TABLE IF NOT EXISTS conversation_provider_event_retention (
    id                INTEGER PRIMARY KEY CHECK (id = 1),
    pruned_through_id INTEGER NOT NULL
);
INSERT OR IGNORE INTO conversation_provider_event_retention (id, pruned_through_id) VALUES (1, 0);

-- +goose Down
DROP TABLE IF EXISTS conversation_provider_event_retention;
