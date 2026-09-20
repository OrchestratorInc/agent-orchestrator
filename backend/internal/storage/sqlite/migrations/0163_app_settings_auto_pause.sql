-- +goose Up
-- +goose StatementBegin
-- Auto-pause exits an agent that has been idle for this many minutes, keeping
-- its session, worktree and conversation so it can be resumed in place. Zero
-- means off: pausing a user's agents unasked is always a deliberate choice.
ALTER TABLE app_settings
    ADD COLUMN auto_pause_idle_minutes INTEGER NOT NULL DEFAULT 0
        CHECK (auto_pause_idle_minutes >= 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_settings DROP COLUMN auto_pause_idle_minutes;
-- +goose StatementEnd
