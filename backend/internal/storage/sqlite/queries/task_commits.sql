-- name: GetTaskCommit :one
SELECT * FROM workflow_task_commits WHERE id=?;

-- name: GetArtifactTaskCommit :one
SELECT * FROM workflow_task_commits WHERE artifact_id=?;

-- name: ListTaskCommits :many
SELECT c.* FROM workflow_task_commits c JOIN workflow_artifacts a ON a.id=c.artifact_id WHERE a.workflow_id=? ORDER BY c.created_at DESC,c.id DESC LIMIT 501;

-- name: InsertTaskCommit :exec
INSERT INTO workflow_task_commits(id,artifact_id,artifact_fingerprint,artifact_revision,policy_digest,gate_id,review_id,parent_sha,tree_sha,commit_sha,target_sha,state,owner_id,epoch,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,'requested',?,?,?);

-- name: FinishTaskCommit :execrows
UPDATE workflow_task_commits SET state=sqlc.arg(state),revision=revision+1
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(expected_revision) AND state IN ('requested','unknown')
  AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch);

-- name: HasPendingTaskCommit :one
SELECT EXISTS(SELECT 1 FROM workflow_task_commits c JOIN workflow_artifacts a ON a.id=c.artifact_id
WHERE a.workflow_id=? AND c.state IN ('requested','unknown'));

-- name: LatestQualityReview :one
SELECT * FROM workflow_quality_reviews WHERE artifact_id=? ORDER BY rowid DESC LIMIT 1;
