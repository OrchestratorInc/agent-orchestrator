-- +goose Up
-- The AO Cloud session a session moved to, as JSON; '' when
-- it never moved. A moved session is archived and must not be restored here.
ALTER TABLE sessions ADD COLUMN moved_to TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN moved_to;
