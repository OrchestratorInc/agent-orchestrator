-- name: InsertQualityReview :exec
INSERT INTO workflow_quality_reviews(id,artifact_id,artifact_fingerprint,policy_revision,policy_digest,target_sha,actor_json,actor_digest,owner_id,epoch,state,root_task_id,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,'running',?,?,?);

-- name: GetQualityReview :one
SELECT * FROM workflow_quality_reviews WHERE id=?;

-- name: ListQualityReviews :many
SELECT r.* FROM workflow_quality_reviews r JOIN workflow_artifacts a ON a.id=r.artifact_id WHERE a.workflow_id=? ORDER BY r.created_at DESC,r.id DESC LIMIT 501;

-- name: FinishQualityReview :execrows
UPDATE workflow_quality_reviews SET state=sqlc.arg(state),verdict=sqlc.arg(verdict),findings_json=sqlc.arg(findings_json),request_digest=sqlc.arg(request_digest),fix_request_id=sqlc.arg(fix_request_id),revision=revision+1,updated_at=sqlc.arg(updated_at)
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(expected_revision) AND state='running' AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch);

-- name: CountQualityFixRequests :one
SELECT count(*) FROM workflow_quality_reviews WHERE root_task_id=? AND fix_request_id<>'';

-- name: CountArtifactFixRequests :one
SELECT count(*) FROM workflow_quality_reviews r JOIN workflow_artifacts a ON a.id=r.artifact_id
WHERE a.attempt_id=? AND a.artifact_revision=? AND r.fix_request_id<>'';

-- name: QualityReviewRootTask :one
WITH RECURSIVE ancestors(task_id) AS (
 SELECT CAST(sqlc.arg(task_id) AS TEXT) AS task_id
 UNION SELECT m.parent_task_id AS task_id FROM workflow_task_materializations m JOIN ancestors a ON m.child_task_id=a.task_id
)
SELECT a.task_id FROM ancestors a WHERE NOT EXISTS(SELECT 1 FROM workflow_task_materializations m WHERE m.child_task_id=a.task_id) LIMIT 1;

-- name: HasApprovedQualityReview :one
SELECT EXISTS(SELECT 1 FROM workflow_quality_reviews r JOIN workflow_artifacts a ON a.id=r.artifact_id
WHERE r.artifact_id=? AND a.state='verified' AND r.artifact_fingerprint=a.fingerprint AND r.policy_digest=? AND r.state='approved'
AND r.rowid=(SELECT max(latest.rowid) FROM workflow_quality_reviews latest WHERE latest.artifact_id=r.artifact_id));
