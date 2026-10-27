-- +goose Up
ALTER TABLE review ADD COLUMN is_archived BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE review DROP COLUMN is_archived;
