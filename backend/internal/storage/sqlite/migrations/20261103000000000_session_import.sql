-- +goose Up
ALTER TABLE sessions ADD COLUMN import_source TEXT NOT NULL DEFAULT '';
-- A terminated session no longer holds its imported conversation, so the same
-- chat can be imported again. FindImportedSession repeats these expressions and
-- this predicate exactly so it can search the index.
CREATE UNIQUE INDEX sessions_import_identity ON sessions(
    CAST(json_extract(NULLIF(import_source, ''), '$.configDir') AS TEXT),
    CAST(json_extract(NULLIF(import_source, ''), '$.nativeId') AS TEXT)
) WHERE import_source <> '' AND is_terminated = 0;
CREATE TABLE session_import_messages (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('user', 'assistant')),
    text TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY(session_id, sequence)
);

-- +goose Down
DROP TABLE session_import_messages;
DROP INDEX sessions_import_identity;
ALTER TABLE sessions DROP COLUMN import_source;
