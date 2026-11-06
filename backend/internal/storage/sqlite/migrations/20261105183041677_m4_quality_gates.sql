-- +goose Up
CREATE TABLE workflow_quality_policies (
 workflow_id TEXT PRIMARY KEY REFERENCES workflows(id), policy_json TEXT NOT NULL CHECK(json_valid(policy_json)),
 policy_digest TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision>0), actor_ref TEXT NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE TABLE workflow_gate_runs (
 id TEXT PRIMARY KEY, artifact_id TEXT NOT NULL REFERENCES workflow_artifacts(id),artifact_fingerprint TEXT NOT NULL,
 policy_digest TEXT NOT NULL,fingerprint TEXT NOT NULL,state TEXT NOT NULL CHECK(state IN ('RUNNING','PASS','FAIL','BLOCKED','UNVERIFIED')),
 results_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(results_json)), owner_id TEXT NOT NULL,epoch INTEGER NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1,created_at TIMESTAMP NOT NULL,expires_at TIMESTAMP NOT NULL
);
CREATE INDEX workflow_gate_artifact ON workflow_gate_runs(artifact_id,created_at);
-- +goose StatementBegin
CREATE TRIGGER workflow_gate_changed AFTER UPDATE ON workflow_gate_runs BEGIN
 INSERT INTO change_log(event_type,payload) SELECT 'workflow_attempt_updated',json_object('workflowId',a.workflow_id,'attemptId',a.attempt_id,'kind','quality_gate','gateId',NEW.id,'state',NEW.state,'revision',NEW.revision) FROM workflow_artifacts a WHERE a.id=NEW.artifact_id;
END;
CREATE TRIGGER workflow_gate_created AFTER INSERT ON workflow_gate_runs BEGIN
 INSERT INTO change_log(event_type,payload) SELECT 'workflow_attempt_updated',json_object('workflowId',a.workflow_id,'attemptId',a.attempt_id,'kind','quality_gate','gateId',NEW.id,'state',NEW.state,'revision',NEW.revision) FROM workflow_artifacts a WHERE a.id=NEW.artifact_id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE workflow_gate_runs;
DROP TABLE workflow_quality_policies;
DELETE FROM change_log WHERE event_type='workflow_attempt_updated' AND json_extract(payload,'$.kind')='quality_gate';
