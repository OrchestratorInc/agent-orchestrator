-- name: GetIntegrationOperation :one
SELECT * FROM workflow_integration_operations WHERE id=?;

-- name: GetPlanIntegrationOperation :one
SELECT * FROM workflow_integration_operations WHERE plan_id=? AND action=?;

-- name: ListIntegrationOperations :many
SELECT * FROM workflow_integration_operations WHERE workflow_id=? ORDER BY created_at DESC,id DESC LIMIT 501;

-- name: InsertIntegrationOperation :exec
INSERT INTO workflow_integration_operations(id,plan_id,workflow_id,action,request_digest,request_json,approval_ref,owner_id,epoch,state,created_at)
VALUES(?,?,?,?,?,?,?,?,?,'requested',?);

-- name: InsertIntegrationRepoOperation :exec
INSERT INTO workflow_integration_repo_operations(operation_id,repo_key,state) VALUES(?,?,'prepared');

-- name: ListIntegrationRepoOperations :many
SELECT * FROM workflow_integration_repo_operations WHERE operation_id=? ORDER BY repo_key;

-- name: GetIntegrationRepoOperation :one
SELECT * FROM workflow_integration_repo_operations WHERE operation_id=? AND repo_key=?;

-- name: SetIntegrationRepoOperation :execrows
UPDATE workflow_integration_repo_operations SET state=sqlc.arg(state),observed_sha=sqlc.arg(observed_sha),revision=revision+1
WHERE operation_id=sqlc.arg(operation_id) AND repo_key=sqlc.arg(repo_key) AND revision=sqlc.arg(expected_revision) AND state=sqlc.arg(expected_state);

-- name: SetIntegrationOperation :execrows
UPDATE workflow_integration_operations SET state=sqlc.arg(state),revision=revision+1
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(expected_revision);

-- name: HasOpenIntegrationOperation :one
SELECT EXISTS(SELECT 1 FROM workflow_integration_operations WHERE workflow_id=? AND state IN ('requested','unknown','partial'));
