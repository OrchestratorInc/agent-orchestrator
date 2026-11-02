-- +goose Up
-- A terminated session no longer holds its imported conversation, so the same
-- chat can be imported again. FindImportedSession repeats these expressions and
-- this predicate exactly so it can search the index.
DROP INDEX sessions_import_identity;
CREATE UNIQUE INDEX sessions_import_identity ON sessions(
    CAST(json_extract(NULLIF(import_source, ''), '$.configDir') AS TEXT),
    CAST(json_extract(NULLIF(import_source, ''), '$.nativeId') AS TEXT)
) WHERE import_source <> '' AND is_terminated = 0;

-- +goose Down
DROP INDEX sessions_import_identity;
CREATE UNIQUE INDEX sessions_import_identity ON sessions(
    CAST(json_extract(NULLIF(import_source, ''), '$.configDir') AS TEXT),
    CAST(json_extract(NULLIF(import_source, ''), '$.nativeId') AS TEXT)
) WHERE import_source <> '';
