-- +goose Up
-- When a session was archived with its artifacts deleted. While set, the
-- session takes no artifact uploads or commits, so an upload that finishes
-- after the archive cannot bring a deleted file back. Restoring the session
-- clears it. NULL for every other session.
ALTER TABLE ao_sessions
    ADD COLUMN IF NOT EXISTS artifacts_deleted_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE ao_sessions DROP COLUMN IF EXISTS artifacts_deleted_at;
