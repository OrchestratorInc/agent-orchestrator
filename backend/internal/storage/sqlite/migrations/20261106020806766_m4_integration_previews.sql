-- +goose Up
CREATE TABLE workflow_integration_plans (
 id TEXT PRIMARY KEY,
 workflow_id TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('clean','conflict','unknown')),
 revision INTEGER NOT NULL DEFAULT 1,
 plan_digest TEXT NOT NULL,
 created_at TIMESTAMP NOT NULL
);

CREATE TABLE workflow_integration_repo_previews (
 plan_id TEXT NOT NULL REFERENCES workflow_integration_plans(id),
 repo_key TEXT NOT NULL,
 artifact_id TEXT NOT NULL REFERENCES workflow_artifacts(id),
 task_commit_id TEXT NOT NULL REFERENCES workflow_task_commits(id),
	artifact_fingerprint TEXT NOT NULL,
	policy_digest TEXT NOT NULL,
 target_ref TEXT NOT NULL,
 expected_target_sha TEXT NOT NULL,
 task_commit_sha TEXT NOT NULL,
 merge_method TEXT NOT NULL CHECK(merge_method='merge'),
 status TEXT NOT NULL CHECK(status IN ('clean','conflict','unknown')),
 tree_sha TEXT NOT NULL,
	merge_commit_sha TEXT NOT NULL,
	diff_digest TEXT NOT NULL,
 conflict_paths_json TEXT NOT NULL,
 preview_digest TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(plan_id,repo_key)
);

-- +goose StatementBegin
CREATE TRIGGER workflow_integration_plan_created AFTER INSERT ON workflow_integration_plans BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('workflow_attempt_updated',json_object('workflowId',NEW.workflow_id,'kind','integration_plan','planId',NEW.id,'status',NEW.status,'revision',NEW.revision));
END;

CREATE TRIGGER workflow_integration_plan_changed AFTER UPDATE ON workflow_integration_plans BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('workflow_attempt_updated',json_object('workflowId',NEW.workflow_id,'kind','integration_plan','planId',NEW.id,'status',NEW.status,'revision',NEW.revision));
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER workflow_integration_plan_changed;
DROP TRIGGER workflow_integration_plan_created;
DROP TABLE workflow_integration_repo_previews;
DROP TABLE workflow_integration_plans;
DELETE FROM change_log WHERE event_type='workflow_attempt_updated' AND json_extract(payload,'$.kind')='integration_plan';
