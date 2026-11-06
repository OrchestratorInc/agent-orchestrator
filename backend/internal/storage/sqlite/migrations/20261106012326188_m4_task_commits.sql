-- +goose Up
CREATE TABLE workflow_task_commits (
 id TEXT PRIMARY KEY, artifact_id TEXT NOT NULL UNIQUE REFERENCES workflow_artifacts(id),
 artifact_fingerprint TEXT NOT NULL, artifact_revision INTEGER NOT NULL,
 policy_digest TEXT NOT NULL, gate_id TEXT NOT NULL REFERENCES workflow_gate_runs(id),
 review_id TEXT NOT NULL REFERENCES workflow_quality_reviews(id),
 parent_sha TEXT NOT NULL, tree_sha TEXT NOT NULL, commit_sha TEXT NOT NULL, target_sha TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('requested','committed','unknown','blocked')),
 owner_id TEXT NOT NULL, epoch INTEGER NOT NULL, revision INTEGER NOT NULL DEFAULT 1,
 created_at TIMESTAMP NOT NULL
);
-- +goose StatementBegin
CREATE TRIGGER workflow_task_commit_created AFTER INSERT ON workflow_task_commits BEGIN
 INSERT INTO change_log(event_type,payload) SELECT 'workflow_attempt_updated',json_object('workflowId',a.workflow_id,'attemptId',a.attempt_id,'kind','task_commit','commitId',NEW.id,'state',NEW.state,'revision',NEW.revision) FROM workflow_artifacts a WHERE a.id=NEW.artifact_id;
END;
CREATE TRIGGER workflow_task_commit_changed AFTER UPDATE ON workflow_task_commits BEGIN
 INSERT INTO change_log(event_type,payload) SELECT 'workflow_attempt_updated',json_object('workflowId',a.workflow_id,'attemptId',a.attempt_id,'kind','task_commit','commitId',NEW.id,'state',NEW.state,'revision',NEW.revision) FROM workflow_artifacts a WHERE a.id=NEW.artifact_id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE workflow_task_commits;
DELETE FROM change_log WHERE event_type='workflow_attempt_updated' AND json_extract(payload,'$.kind')='task_commit';
