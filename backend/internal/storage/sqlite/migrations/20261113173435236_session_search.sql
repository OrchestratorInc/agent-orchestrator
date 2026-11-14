-- +goose Up
-- Session search keeps its full-text index in its own file, search.db
-- (storage/sqlite/search_db.go). SQLite allows one writer per database file,
-- and every write the daemon makes (chat streaming, session state) goes
-- through ao.db's, so an index in ao.db would make those wait. The index is
-- derived data: the indexer builds it in search.db from the sessions, and it
-- is READ-ONLY toward session data.
--
-- ao.db holds only what a session delete must record in its own transaction,
-- and the token that ties a search.db to this database.

-- +goose StatementBegin
-- A deleted session's id, a single row written by the delete itself, whatever
-- deletes the session. Search ignores that session's documents at once and the
-- indexer removes them from search.db afterwards. Session ids are never reused
-- (session_num_floors), so a recorded id stays deleted.
CREATE TABLE session_search_deleted (
    session_id TEXT PRIMARY KEY
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER sessions_search_ad AFTER DELETE ON sessions BEGIN
    INSERT OR IGNORE INTO session_search_deleted (session_id) VALUES (old.id);
END;
-- +goose StatementEnd
-- +goose StatementBegin
-- The token search.db records as the database it indexes. A search.db beside
-- a different ao.db (a restored or swapped database) holds another token and
-- is cleared and rebuilt, instead of describing sessions that do not exist.
CREATE TABLE session_search_owner (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    token TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_owner;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sessions_search_ad;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_deleted;
-- +goose StatementEnd
