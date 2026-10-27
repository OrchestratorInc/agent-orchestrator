-- +goose Up
-- The guest a session was started for, when someone a project was shared
-- with asked its orchestrator for it. NULL for every other session. That
-- guest may write in the session; others in the project share only watch.
ALTER TABLE ao_sessions
    ADD COLUMN requested_by_user_id UUID REFERENCES ao_users(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE ao_sessions DROP COLUMN requested_by_user_id;
