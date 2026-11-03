-- +goose Up
-- Product-telemetry facts about a session that the sessions row does not hold:
-- who caused it to exist, and how often it stopped to wait on the user. Read
-- once when the session ends (ao.session.ended). Kept out of sessions so the
-- hot session read/write paths do not carry analytics columns. Rows for
-- sessions created before this migration are absent; readers treat that as
-- unknown rather than inventing a creator.
CREATE TABLE session_telemetry (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    created_by TEXT NOT NULL DEFAULT '',
    waiting_input_count INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE session_telemetry;
