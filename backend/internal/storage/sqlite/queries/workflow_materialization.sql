-- name: GetTaskMaterialization :one
SELECT * FROM workflow_task_materializations WHERE request_id=?;

-- name: GetTaskMaterializationKey :one
SELECT * FROM workflow_task_materialization_keys WHERE idempotency_key=?;

-- name: InsertTaskMaterializationKey :exec
INSERT INTO workflow_task_materialization_keys(idempotency_key,request_fingerprint,request_id) VALUES(?,?,?);

-- name: InsertTaskMaterialization :exec
INSERT INTO workflow_task_materializations(request_id,workflow_id,parent_task_id,child_task_id,request_sha,request_revision,source_workflow_revision,workflow_revision,actor_ref,reason,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?);

-- name: AdvanceMaterializationWorkflow :execrows
UPDATE workflows SET revision=revision+1,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(workflow_id) AND revision=sqlc.arg(expected_revision)
AND revision<9223372036854775807 AND state IN ('active','paused');

-- name: ListTaskBudgetAncestors :many
WITH RECURSIVE ancestors(task_id) AS (
 SELECT CAST(sqlc.arg(task_id) AS TEXT) AS task_id
 UNION
 SELECT m.parent_task_id AS task_id FROM workflow_task_materializations m JOIN ancestors a ON m.child_task_id=a.task_id
)
SELECT a.task_id FROM ancestors a;

-- name: CountMaterializedChildren :one
SELECT count(*) FROM workflow_task_materializations WHERE parent_task_id=?;

-- name: ListMaterializationTaskRepositories :many
SELECT * FROM workflow_task_repos WHERE workflow_id=? ORDER BY task_id,repo_key;
