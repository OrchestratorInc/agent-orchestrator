-- +goose Up
ALTER TABLE ao_sessions
    ADD COLUMN task_naming_state TEXT NOT NULL DEFAULT 'done'
        CHECK (task_naming_state IN ('pending', 'reserved', 'done')),
    ADD COLUMN task_name_slug TEXT NOT NULL DEFAULT '',
    ADD COLUMN task_name_initial_title TEXT,
    ADD COLUMN task_name_generation_at TIMESTAMPTZ;

CREATE INDEX ao_sessions_task_name_generation
    ON ao_sessions (org_id, task_name_generation_at)
    WHERE task_naming_state = 'pending' AND NOT is_terminated AND agent_session_id = '';

-- +goose Down
DROP INDEX ao_sessions_task_name_generation;
ALTER TABLE ao_sessions
    DROP COLUMN task_name_generation_at,
    DROP COLUMN task_name_initial_title,
    DROP COLUMN task_name_slug,
    DROP COLUMN task_naming_state;
