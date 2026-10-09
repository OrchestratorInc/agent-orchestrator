-- The latest user-facing reason a sandbox has not started (for example a
-- bring-your-own Coder workspace that never became ready, or a CPU
-- architecture AO has no worker for). last_error carries operator detail and is
-- overwritten by every observation; these columns keep a stable code and a
-- human message the session API exposes until the worker first checks in or
-- the user retries. Columns on an existing tenant table inherit its RLS
-- policies and grants, so no policy changes are needed.
-- +goose Up
ALTER TABLE ao_sandboxes
    ADD COLUMN IF NOT EXISTS startup_error_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS startup_error_message TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS startup_error_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE ao_sandboxes
    DROP COLUMN IF EXISTS startup_error_at,
    DROP COLUMN IF EXISTS startup_error_message,
    DROP COLUMN IF EXISTS startup_error_code;
