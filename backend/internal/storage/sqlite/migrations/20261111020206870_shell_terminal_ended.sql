-- +goose Up
-- A session's user shell whose process is gone (machine reboot, host crash,
-- typed `exit`) is kept as an ended tab instead of being deleted, so the user
-- can see it existed and reopen it. NULL means the shell is not known to have ended.
ALTER TABLE shell_terminals ADD COLUMN ended_at TIMESTAMP;
-- The coding agent last seen running in the shell, so an ended tab can offer to
-- resume that agent's conversations. Empty when none was seen.
ALTER TABLE shell_terminals ADD COLUMN last_agent_harness TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE shell_terminals DROP COLUMN last_agent_harness;
ALTER TABLE shell_terminals DROP COLUMN ended_at;
