-- name: InsertIntegrationPlan :exec
INSERT INTO workflow_integration_plans(id,workflow_id,status,revision,plan_digest,created_at)
VALUES(?,?,?,?,?,?);

-- name: GetIntegrationPlan :one
SELECT * FROM workflow_integration_plans WHERE id=?;

-- name: InsertIntegrationRepoPreview :exec
INSERT INTO workflow_integration_repo_previews(plan_id,repo_key,artifact_id,task_commit_id,artifact_fingerprint,policy_digest,target_ref,expected_target_sha,task_commit_sha,merge_method,status,tree_sha,merge_commit_sha,diff_digest,conflict_paths_json,preview_digest,revision)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?);

-- name: GetIntegrationRepoPreviews :many
SELECT * FROM workflow_integration_repo_previews WHERE plan_id=? ORDER BY repo_key;

-- name: ListIntegrationPlans :many
SELECT * FROM workflow_integration_plans WHERE workflow_id=? ORDER BY created_at DESC,id DESC LIMIT 501;
