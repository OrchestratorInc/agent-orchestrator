-- +goose Up

-- The person a terminal ticket was issued to, so the agent terminal can tell
-- whose keystrokes it receives and admit only the composer lease holder's in
-- a shared session. NULL on tickets issued before this column existed and on
-- tickets that are not a person's.
ALTER TABLE ao_access_tickets
    ADD COLUMN user_id UUID REFERENCES ao_users(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE ao_access_tickets DROP COLUMN IF EXISTS user_id;
