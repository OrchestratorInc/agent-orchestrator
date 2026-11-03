-- +goose Up

-- A single durable idempotency key protects all workflow write operations.
-- The resource is resolved by result_type/result_id; the request fingerprint
-- prevents a caller from reusing the same key for a different command.
CREATE TABLE workflow_idempotency (
    idempotency_key      TEXT PRIMARY KEY NOT NULL CHECK (length(trim(idempotency_key)) > 0),
    workflow_id          TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    operation            TEXT NOT NULL CHECK (operation IN (
        'workflow.create', 'workflow.activate', 'task.claim',
        'attempt.start', 'attempt.settle', 'task.retry', 'attempt.resolve'
    )),
    request_fingerprint  TEXT NOT NULL CHECK (length(request_fingerprint) = 64),
    result_type          TEXT NOT NULL CHECK (result_type IN ('workflow', 'task', 'attempt')),
    result_id            TEXT NOT NULL CHECK (length(trim(result_id)) > 0),
    created_at           TIMESTAMP NOT NULL
);

CREATE INDEX idx_workflow_idempotency_workflow
    ON workflow_idempotency (workflow_id, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_workflow_idempotency_workflow;
DROP TABLE IF EXISTS workflow_idempotency;
