-- +goose Up
CREATE TABLE workflow_artifacts (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL, task_id TEXT NOT NULL, attempt_id TEXT NOT NULL,
    session_id TEXT NOT NULL REFERENCES sessions(id), generation TEXT NOT NULL CHECK(generation<>''),
    repo_key TEXT NOT NULL, artifact_revision INTEGER NOT NULL CHECK(artifact_revision>0),
    fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64),
    snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
    repo_path TEXT NOT NULL, worktree_path TEXT NOT NULL, git_common_dir TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('verified','stale')),
    revision INTEGER NOT NULL CHECK(revision>0), created_at TIMESTAMP NOT NULL,
    UNIQUE(attempt_id,repo_key,artifact_revision),
    FOREIGN KEY(attempt_id,workflow_id,task_id) REFERENCES workflow_attempts(id,workflow_id,task_id),
    FOREIGN KEY(workflow_id,task_id,repo_key) REFERENCES workflow_task_repos(workflow_id,task_id,repo_key)
);
CREATE INDEX workflow_artifacts_workflow ON workflow_artifacts(workflow_id,attempt_id,repo_key,artifact_revision);

CREATE TABLE workflow_task_path_reservations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    admission_id TEXT NOT NULL REFERENCES scheduler_admissions(id),
    attempt_id TEXT NOT NULL, workflow_id TEXT NOT NULL, task_id TEXT NOT NULL, repo_key TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id), repo_path TEXT NOT NULL, repo_identity TEXT NOT NULL, scope_path TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '', generation TEXT NOT NULL DEFAULT '',
    owner_id TEXT NOT NULL, epoch INTEGER NOT NULL CHECK(epoch>0),
    lease_until TIMESTAMP NOT NULL, revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
    released_at TIMESTAMP,
    UNIQUE(attempt_id,repo_key,scope_path),
    FOREIGN KEY(attempt_id,workflow_id,task_id) REFERENCES workflow_attempts(id,workflow_id,task_id),
    FOREIGN KEY(workflow_id,task_id,repo_key) REFERENCES workflow_task_repos(workflow_id,task_id,repo_key)
);
CREATE INDEX workflow_paths_occupied ON workflow_task_path_reservations(repo_identity) WHERE released_at IS NULL;

-- +goose StatementBegin
CREATE TRIGGER workflow_artifact_immutable BEFORE UPDATE OF
    id,workflow_id,task_id,attempt_id,session_id,generation,repo_key,artifact_revision,
    fingerprint,snapshot_json,repo_path,worktree_path,git_common_dir,created_at ON workflow_artifacts
BEGIN SELECT RAISE(ABORT,'artifact identity is immutable'); END;
CREATE TRIGGER workflow_artifact_created AFTER INSERT ON workflow_artifacts BEGIN
    INSERT INTO change_log(event_type,payload) VALUES('workflow_attempt_updated',json_object(
        'workflowId',NEW.workflow_id,'attemptId',NEW.attempt_id,'kind','artifact',
        'artifactId',NEW.id,'state',NEW.state,'revision',NEW.revision));
END;
CREATE TRIGGER workflow_artifact_updated AFTER UPDATE ON workflow_artifacts BEGIN
    INSERT INTO change_log(event_type,payload) VALUES('workflow_attempt_updated',json_object(
        'workflowId',NEW.workflow_id,'attemptId',NEW.attempt_id,'kind','artifact',
        'artifactId',NEW.id,'state',NEW.state,'revision',NEW.revision));
END;
CREATE TRIGGER workflow_path_reserved AFTER INSERT ON workflow_task_path_reservations BEGIN
    INSERT INTO change_log(event_type,payload) VALUES('workflow_attempt_updated',json_object(
        'workflowId',NEW.workflow_id,'attemptId',NEW.attempt_id,'kind','path_reservation',
        'revision',NEW.revision,'epoch',NEW.epoch,'released',NEW.released_at IS NOT NULL));
END;
CREATE TRIGGER workflow_path_updated AFTER UPDATE ON workflow_task_path_reservations BEGIN
    INSERT INTO change_log(event_type,payload) VALUES('workflow_attempt_updated',json_object(
        'workflowId',NEW.workflow_id,'attemptId',NEW.attempt_id,'kind','path_reservation',
        'revision',NEW.revision,'epoch',NEW.epoch,'released',NEW.released_at IS NOT NULL));
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE workflow_task_path_reservations;
DROP TABLE workflow_artifacts;
DELETE FROM change_log WHERE event_type='workflow_attempt_updated'
    AND json_extract(payload,'$.kind') IN ('artifact','path_reservation');
