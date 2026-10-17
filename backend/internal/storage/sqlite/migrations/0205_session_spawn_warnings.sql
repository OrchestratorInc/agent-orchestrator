-- +goose Up
-- Non-fatal warnings recorded while a session was spawned, stored as a JSON
-- array of messages (for example, a default-branch fetch that failed so the
-- worktree may start from a stale local base). Written once after spawn; the
-- session change trigger is intentionally left alone.
ALTER TABLE sessions ADD COLUMN spawn_warnings TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN spawn_warnings;
