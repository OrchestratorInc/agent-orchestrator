-- +goose Up
ALTER TABLE app_settings ADD COLUMN cursor_usage INTEGER NOT NULL DEFAULT 0
    CHECK (cursor_usage IN (0, 1));

CREATE TABLE usage_cursor_reads (
    account_key TEXT PRIMARY KEY CHECK (trim(account_key) <> ''),
    read_until  TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE usage_cursor_reads;
ALTER TABLE app_settings DROP COLUMN cursor_usage;
