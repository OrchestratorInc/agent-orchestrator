-- +goose Up
ALTER TABLE workflow_tasks ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_tasks ADD COLUMN effort TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_tasks ADD COLUMN profile TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_tasks ADD COLUMN planner BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE workflow_plan_candidates (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL REFERENCES workflows(id),
    source_attempt_id TEXT NOT NULL REFERENCES workflow_attempts(id),
    source_turn_id TEXT NOT NULL,
    stop_intervention_id TEXT NOT NULL DEFAULT '',
    expected_revision INTEGER NOT NULL,
    digest TEXT NOT NULL,
    definition_json TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    request_fingerprint TEXT NOT NULL,
    state_fingerprint TEXT NOT NULL,
    confirmed_revision INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE TABLE workflow_replans (
    planner_task_id TEXT PRIMARY KEY REFERENCES workflow_tasks(id),
    workflow_id TEXT NOT NULL REFERENCES workflows(id),
    stop_intervention_id TEXT NOT NULL REFERENCES workflow_interventions(id),
    stopped_attempt_id TEXT NOT NULL REFERENCES workflow_attempts(id),
    idempotency_key TEXT NOT NULL UNIQUE,
    request_fingerprint TEXT NOT NULL
);

-- +goose StatementBegin
CREATE TRIGGER workflow_plan_candidate_created AFTER INSERT ON workflow_plan_candidates
BEGIN
    INSERT INTO change_log(project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_updated', json_object('workflowId', NEW.workflow_id, 'kind', 'plan_candidate'), NEW.created_at);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS workflow_plan_candidate_created;
DROP TABLE IF EXISTS workflow_plan_candidates;
DROP TABLE IF EXISTS workflow_replans;
ALTER TABLE workflow_tasks DROP COLUMN planner;
ALTER TABLE workflow_tasks DROP COLUMN profile;
ALTER TABLE workflow_tasks DROP COLUMN effort;
ALTER TABLE workflow_tasks DROP COLUMN model;
