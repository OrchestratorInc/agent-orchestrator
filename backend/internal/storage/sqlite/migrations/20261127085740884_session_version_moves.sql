-- +goose Up
CREATE TABLE session_version_moves (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    intent TEXT NOT NULL,
    stage TEXT NOT NULL CHECK (stage IN ('stopping', 'starting', 'retry')),
    attempts INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE session_version_moves;
