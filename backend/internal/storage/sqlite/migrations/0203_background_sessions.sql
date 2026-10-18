-- Summary: mark sessions that run in the background, such as dreams, so the desktop lists can leave them out until they need the user.
-- +goose Up
ALTER TABLE sessions ADD COLUMN background BOOLEAN NOT NULL DEFAULT FALSE CHECK (background IN (0, 1));

-- +goose Down
ALTER TABLE sessions DROP COLUMN background;
