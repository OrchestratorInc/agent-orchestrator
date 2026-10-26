-- +goose Up
-- The launch whose terminal AO destroyed on purpose, when an agent exit keeps
-- its session (ExitAgent). That runtime's death afterwards is not a crash, so
-- the reaper must not end the session. A new launch has a new id, so the value
-- never outlives the launch it names.
ALTER TABLE sessions ADD COLUMN runtime_released_launch_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN runtime_released_launch_id;
