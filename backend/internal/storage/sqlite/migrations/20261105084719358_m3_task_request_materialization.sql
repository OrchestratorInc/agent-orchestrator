-- +goose Up
CREATE TABLE workflow_task_materializations (
 request_id TEXT PRIMARY KEY REFERENCES workflow_task_requests(id),
 workflow_id TEXT NOT NULL REFERENCES workflows(id),
 parent_task_id TEXT NOT NULL, child_task_id TEXT NOT NULL UNIQUE,
 request_sha TEXT NOT NULL CHECK(length(request_sha)=64),
 request_revision INTEGER NOT NULL CHECK(request_revision>0),
 source_workflow_revision INTEGER NOT NULL CHECK(source_workflow_revision>0),
 workflow_revision INTEGER NOT NULL CHECK(workflow_revision=source_workflow_revision+1),
 actor_ref TEXT NOT NULL CHECK(length(actor_ref)>0), reason TEXT NOT NULL,
 created_at TIMESTAMP NOT NULL,
 FOREIGN KEY(workflow_id,parent_task_id) REFERENCES workflow_tasks(workflow_id,id),
 FOREIGN KEY(workflow_id,child_task_id) REFERENCES workflow_tasks(workflow_id,id),
 CHECK(parent_task_id<>child_task_id)
);
CREATE INDEX workflow_task_materializations_parent ON workflow_task_materializations(parent_task_id);
CREATE TABLE workflow_task_materialization_keys (
 idempotency_key TEXT PRIMARY KEY CHECK(length(idempotency_key)>0),
 request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64),
 request_id TEXT NOT NULL REFERENCES workflow_task_materializations(request_id)
);
-- +goose StatementBegin
CREATE TRIGGER workflow_task_materialization_immutable BEFORE UPDATE ON workflow_task_materializations BEGIN
 SELECT RAISE(ABORT,'task materialization is immutable');
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE workflow_task_materialization_keys;
DROP TABLE workflow_task_materializations;
