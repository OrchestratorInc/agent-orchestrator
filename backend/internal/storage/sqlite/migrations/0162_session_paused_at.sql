-- A deliberately paused agent (user, idle policy, memory pressure) must stay
-- distinguishable from one that crashed: both leave activity_state = 'exited',
-- so the intent is recorded beside it. Cleared when the agent is resumed.
-- No trigger change: every pause or resume also flips activity_state or
-- runtime_launch_id, which already fires sessions_cdc_update.

-- +goose Up
ALTER TABLE sessions ADD COLUMN paused_at TIMESTAMP;
ALTER TABLE sessions ADD COLUMN pause_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN paused_at;
ALTER TABLE sessions DROP COLUMN pause_reason;
