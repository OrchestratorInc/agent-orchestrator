-- Summary: let an automation start when a worker session ends, and record which session started each run.
-- +goose Up
ALTER TABLE automations ADD COLUMN trigger_type TEXT NOT NULL DEFAULT 'schedule' CHECK (trigger_type IN ('schedule', 'session_ended'));
ALTER TABLE automation_runs ADD COLUMN trigger_session_id TEXT REFERENCES sessions (id) ON DELETE SET NULL;
CREATE UNIQUE INDEX idx_automation_runs_trigger_session
    ON automation_runs (automation_id, trigger_session_id)
    WHERE trigger_session_id IS NOT NULL;

-- +goose Down
DROP INDEX idx_automation_runs_trigger_session;
ALTER TABLE automation_runs DROP COLUMN trigger_session_id;
ALTER TABLE automations DROP COLUMN trigger_type;
