-- +goose Up
CREATE TABLE timestamp_repair_progress (
    table_name TEXT PRIMARY KEY,
    signature TEXT NOT NULL DEFAULT '',
    generation INTEGER NOT NULL DEFAULT 0,
    completed INTEGER NOT NULL DEFAULT 0,
    rejected INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE timestamp_repair_control (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    paused INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1))
);
INSERT INTO timestamp_repair_control(id) VALUES(1);

-- +goose Down
-- Keep the tracking tables on downgrade: installed SQL triggers must continue
-- invalidating completion when an older binary writes legacy timestamps.
SELECT 1;
