-- +goose Up
-- Where a session moved to (another AO machine or AO Cloud), as JSON; '' when
-- it never moved. A moved session is archived and must not be restored here.
ALTER TABLE sessions ADD COLUMN moved_to TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN moved_to;
