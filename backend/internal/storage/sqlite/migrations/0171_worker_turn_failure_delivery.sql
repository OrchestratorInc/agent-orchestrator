-- +goose Up
-- +goose StatementBegin
CREATE TABLE worker_turn_failure_delivery (
    turn_id TEXT PRIMARY KEY REFERENCES conversation_turns(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL,
    next_attempt_at TIMESTAMP NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    -- Pin the first attempted recipient before sending. A lost acknowledgement
    -- must not move the same semantic key to another orchestrator session.
    target_session_id TEXT,
    accepted_at TIMESTAMP,
    last_error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_worker_turn_failure_delivery_due
    ON worker_turn_failure_delivery(accepted_at, next_attempt_at, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_worker_turn_failure_delivery_due;
DROP TABLE IF EXISTS worker_turn_failure_delivery;
-- +goose StatementEnd
