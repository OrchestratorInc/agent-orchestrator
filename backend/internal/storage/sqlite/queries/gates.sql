-- name: GetWorkflowQualityPolicy :one
SELECT * FROM workflow_quality_policies WHERE workflow_id=?;

-- name: SetWorkflowQualityPolicy :execrows
INSERT INTO workflow_quality_policies(workflow_id,policy_json,policy_digest,revision,actor_ref,updated_at)
VALUES(?,?,?,1,?,?) ON CONFLICT(workflow_id) DO NOTHING;

-- name: UpdateWorkflowQualityPolicy :execrows
UPDATE workflow_quality_policies SET policy_json=sqlc.arg(policy_json),policy_digest=sqlc.arg(policy_digest),revision=revision+1,actor_ref=sqlc.arg(actor_ref),updated_at=sqlc.arg(updated_at)
WHERE workflow_id=sqlc.arg(workflow_id) AND revision=sqlc.arg(expected_revision);

-- name: InsertGateRun :exec
INSERT INTO workflow_gate_runs(id,artifact_id,artifact_fingerprint,policy_digest,fingerprint,state,owner_id,epoch,created_at,expires_at) VALUES(?,?,?,?,?,'RUNNING',?,?,?,?);

-- name: GetGateRun :one
SELECT * FROM workflow_gate_runs WHERE id=?;

-- name: ListGateRuns :many
SELECT g.* FROM workflow_gate_runs g JOIN workflow_artifacts a ON a.id=g.artifact_id WHERE a.workflow_id=? ORDER BY g.created_at DESC,g.id DESC LIMIT 501;

-- name: FinishGateRun :execrows
UPDATE workflow_gate_runs SET state=sqlc.arg(state),results_json=sqlc.arg(results_json),revision=revision+1
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(expected_revision) AND state='RUNNING' AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch);

-- name: LatestGateRun :one
SELECT * FROM workflow_gate_runs WHERE artifact_id=? ORDER BY rowid DESC LIMIT 1;

-- name: HasUnresolvedGateRun :one
SELECT EXISTS(SELECT 1 FROM workflow_gate_runs g JOIN workflow_artifacts a ON a.id=g.artifact_id
WHERE a.attempt_id=(SELECT current_artifact.attempt_id FROM workflow_artifacts current_artifact WHERE current_artifact.id=?) AND g.state IN ('RUNNING','UNVERIFIED'));

-- name: LatestTaskRepoArtifact :one
SELECT a.* FROM workflow_artifacts a WHERE a.attempt_id=(SELECT wa.id FROM workflow_attempts wa WHERE wa.workflow_id=sqlc.arg(workflow_id) AND wa.task_id=sqlc.arg(task_id) ORDER BY wa.attempt_no DESC LIMIT 1)
AND a.repo_key=sqlc.arg(repo_key) ORDER BY a.artifact_revision DESC LIMIT 1;
