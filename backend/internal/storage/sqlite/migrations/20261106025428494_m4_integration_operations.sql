-- +goose Up
CREATE TABLE workflow_integration_operations (
 id TEXT PRIMARY KEY,
 plan_id TEXT NOT NULL REFERENCES workflow_integration_plans(id),
 workflow_id TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('apply','push')),
 request_digest TEXT NOT NULL,

 request_json TEXT NOT NULL,
 approval_ref TEXT NOT NULL,
 owner_id TEXT NOT NULL, epoch INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('requested','applied','rejected','partial','unknown')),
 revision INTEGER NOT NULL DEFAULT 1,
 created_at TIMESTAMP NOT NULL,
 UNIQUE(plan_id,action)
);
CREATE UNIQUE INDEX workflow_integration_active ON workflow_integration_operations(workflow_id)
WHERE state IN ('requested','unknown','partial');
CREATE TABLE workflow_integration_repo_operations (
 operation_id TEXT NOT NULL REFERENCES workflow_integration_operations(id),
 repo_key TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('prepared','requested','applied','rejected','unknown')),
 observed_sha TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(operation_id,repo_key)
);
-- +goose StatementBegin
CREATE TRIGGER workflow_integration_operation_created AFTER INSERT ON workflow_integration_operations BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('workflow_updated',json_object('workflowId',NEW.workflow_id,'kind','integration_operation','operationId',NEW.id,'state',NEW.state,'revision',NEW.revision));
END;
CREATE TRIGGER workflow_integration_operation_changed AFTER UPDATE ON workflow_integration_operations BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('workflow_updated',json_object('workflowId',NEW.workflow_id,'kind','integration_operation','operationId',NEW.id,'state',NEW.state,'revision',NEW.revision));
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE workflow_integration_repo_operations;
DROP TABLE workflow_integration_operations;
DELETE FROM change_log WHERE event_type='workflow_updated' AND json_extract(payload,'$.kind')='integration_operation';
