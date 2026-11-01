-- +goose Up
ALTER TABLE shell_terminals ADD COLUMN command_owner TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE shell_terminals DROP COLUMN command_owner;
