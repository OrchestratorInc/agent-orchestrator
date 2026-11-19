-- +goose Up
-- Full-text index over finished chat messages for command-palette search.
-- External content: the index stores only tokens and reads text back from
-- conversation_messages, keyed by its rowid. Streaming messages stay out until
-- they finish, so a long reply is indexed once, not on every delta.
-- A future rebuild of conversation_messages that renumbers rowids must also
-- run INSERT INTO conversation_messages_fts(conversation_messages_fts) VALUES('rebuild').
CREATE VIRTUAL TABLE conversation_messages_fts USING fts5(
    text,
    content = 'conversation_messages',
    content_rowid = 'rowid',
    tokenize = 'porter unicode61'
);

INSERT INTO conversation_messages_fts(rowid, text)
SELECT rowid, text FROM conversation_messages WHERE streaming = 0;

-- +goose StatementBegin
CREATE TRIGGER conversation_messages_fts_insert
AFTER INSERT ON conversation_messages
WHEN new.streaming = 0
BEGIN
    INSERT INTO conversation_messages_fts(rowid, text) VALUES (new.rowid, new.text);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER conversation_messages_fts_update
AFTER UPDATE OF text, streaming ON conversation_messages
WHEN old.streaming = 0 OR new.streaming = 0
BEGIN
    INSERT INTO conversation_messages_fts(conversation_messages_fts, rowid, text)
    SELECT 'delete', old.rowid, old.text WHERE old.streaming = 0;
    INSERT INTO conversation_messages_fts(rowid, text)
    SELECT new.rowid, new.text WHERE new.streaming = 0;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER conversation_messages_fts_delete
AFTER DELETE ON conversation_messages
WHEN old.streaming = 0
BEGIN
    INSERT INTO conversation_messages_fts(conversation_messages_fts, rowid, text)
    VALUES ('delete', old.rowid, old.text);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS conversation_messages_fts_delete;
DROP TRIGGER IF EXISTS conversation_messages_fts_update;
DROP TRIGGER IF EXISTS conversation_messages_fts_insert;
DROP TABLE IF EXISTS conversation_messages_fts;
