-- +goose Up
CREATE TABLE workflow_quality_reviews (
 id TEXT PRIMARY KEY, artifact_id TEXT NOT NULL REFERENCES workflow_artifacts(id),
 artifact_fingerprint TEXT NOT NULL, policy_revision INTEGER NOT NULL CHECK(policy_revision>0),policy_digest TEXT NOT NULL,
 target_sha TEXT NOT NULL, actor_json TEXT NOT NULL CHECK(json_valid(actor_json)),
 actor_digest TEXT NOT NULL, owner_id TEXT NOT NULL, epoch INTEGER NOT NULL CHECK(epoch>0),
 state TEXT NOT NULL CHECK(state IN ('running','approved','changes_requested','unverified','blocked')),
 verdict TEXT NOT NULL DEFAULT '', findings_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(findings_json)),
 request_digest TEXT NOT NULL DEFAULT '', root_task_id TEXT NOT NULL,
 fix_request_id TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 1,
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE INDEX workflow_review_artifact ON workflow_quality_reviews(artifact_id,created_at);
CREATE INDEX workflow_review_fix_root ON workflow_quality_reviews(root_task_id) WHERE fix_request_id<>'';
-- +goose StatementBegin
CREATE TRIGGER workflow_quality_review_changed AFTER UPDATE ON workflow_quality_reviews BEGIN
 INSERT INTO change_log(event_type,payload) SELECT 'workflow_attempt_updated',json_object(
 'workflowId',a.workflow_id,'attemptId',a.attempt_id,'kind','quality_review','reviewId',NEW.id,'state',NEW.state,'revision',NEW.revision)
 FROM workflow_artifacts a WHERE a.id=NEW.artifact_id;
END;
CREATE TRIGGER workflow_quality_review_created AFTER INSERT ON workflow_quality_reviews BEGIN
 INSERT INTO change_log(event_type,payload) SELECT 'workflow_attempt_updated',json_object(
 'workflowId',a.workflow_id,'attemptId',a.attempt_id,'kind','quality_review','reviewId',NEW.id,'state',NEW.state,'revision',NEW.revision)
 FROM workflow_artifacts a WHERE a.id=NEW.artifact_id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE workflow_quality_reviews;
DELETE FROM change_log WHERE event_type='workflow_attempt_updated' AND json_extract(payload,'$.kind')='quality_review';
