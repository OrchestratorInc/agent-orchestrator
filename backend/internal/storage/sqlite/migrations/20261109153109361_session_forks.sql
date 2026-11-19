-- +goose Up
-- A forked session records where it came from. Rows are kept after either
-- session is deleted: the fork's marker still names its source, and numbering
-- ("Fork 2 of ...") never reuses a number. There is deliberately no foreign key.
CREATE TABLE session_forks (
    fork_session_id   TEXT PRIMARY KEY,
    source_session_id TEXT NOT NULL,
    root_session_id   TEXT NOT NULL,
    source_turn_id    TEXT NOT NULL DEFAULT '',
    source_title      TEXT NOT NULL DEFAULT '',
    context_mode      TEXT NOT NULL DEFAULT '',
    fork_number       INTEGER NOT NULL,
    created_at        TIMESTAMP NOT NULL
);
CREATE INDEX idx_session_forks_source ON session_forks(source_session_id);
CREATE INDEX idx_session_forks_root ON session_forks(root_session_id);

-- +goose Down
DROP TABLE session_forks;
