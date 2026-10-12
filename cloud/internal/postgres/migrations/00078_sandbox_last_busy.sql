-- +goose Up

-- The last heartbeat in which the worker saw activity in the sandbox: terminal
-- output, a process under a workspace shell, or CPU use beyond its own. Idle
-- pause requires this, like the last interaction and chat message, to be older
-- than the idle threshold, so a build, test run or dev server keeps a session
-- awake even when nobody is typing.
ALTER TABLE ao_sandboxes ADD COLUMN last_busy_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE ao_sandboxes DROP COLUMN last_busy_at;
