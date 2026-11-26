-- +goose Up
-- +goose StatementBegin
CREATE TABLE worker_turn_failure_delivery (
    turn_id TEXT PRIMARY KEY REFERENCES conversation_turns(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL,
    next_attempt_at TIMESTAMP NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    -- Set once a send may have been accepted (it succeeded or its outcome is
    -- uncertain), so a retry with the same semantic key reaches the same
    -- orchestrator. Cleared when that orchestrator is terminated or replaced.
    target_session_id TEXT,
    accepted_at TIMESTAMP,
    last_error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_worker_turn_failure_delivery_due
    ON worker_turn_failure_delivery(accepted_at, next_attempt_at, created_at);

-- Session and project deletes cascade here; index their lookups.
CREATE INDEX idx_worker_turn_failure_delivery_session
    ON worker_turn_failure_delivery(session_id);
CREATE INDEX idx_worker_turn_failure_delivery_project
    ON worker_turn_failure_delivery(project_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_worker_turn_failure_delivery_project;
DROP INDEX IF EXISTS idx_worker_turn_failure_delivery_session;
DROP INDEX IF EXISTS idx_worker_turn_failure_delivery_due;
DROP TABLE IF EXISTS worker_turn_failure_delivery;
-- +goose StatementEnd
