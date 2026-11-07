-- +goose Up
-- Reading Cursor usage sends the user's Cursor login to cursor.com, so it is
-- off until the user turns it on.
ALTER TABLE app_settings ADD COLUMN cursor_usage INTEGER NOT NULL DEFAULT 0
    CHECK (cursor_usage IN (0, 1));

-- How far Cursor's account usage has been read. One row: a read for another
-- account starts over.
CREATE TABLE usage_cursor_reads (
    account_key TEXT PRIMARY KEY CHECK (trim(account_key) <> ''),
    read_until  TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE usage_cursor_reads;
ALTER TABLE app_settings DROP COLUMN cursor_usage;
