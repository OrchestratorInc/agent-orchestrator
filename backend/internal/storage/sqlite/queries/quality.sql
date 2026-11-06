-- name: InsertArtifact :exec
INSERT INTO workflow_artifacts(id,workflow_id,task_id,attempt_id,session_id,generation,repo_key,
    artifact_revision,fingerprint,snapshot_json,repo_path,worktree_path,git_common_dir,state,revision,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'verified',1,?);

-- name: GetArtifact :one
SELECT * FROM workflow_artifacts WHERE id=?;

-- name: GetArtifactVersion :one
SELECT * FROM workflow_artifacts WHERE attempt_id=? AND repo_key=? AND artifact_revision=?;

-- name: ListArtifacts :many
SELECT * FROM workflow_artifacts WHERE workflow_id=? ORDER BY created_at DESC,id DESC LIMIT 501;

-- name: ListWorkflowPathReservations :many
SELECT id,attempt_id,task_id,repo_key,scope_path,session_id,lease_until,revision,released_at
FROM workflow_task_path_reservations WHERE workflow_id=? ORDER BY task_id,repo_key,scope_path,id;

-- name: RecentCoordinationMessages :many
SELECT id,COALESCE(from_task_id,'') AS from_task,COALESCE(to_task_id,'') AS to_task,kind,delivery_state,revision,created_at
FROM workflow_messages WHERE workflow_id=? ORDER BY created_at DESC,id DESC LIMIT 50;

-- name: LatestTaskSchedulingReason :one
SELECT reason_code FROM scheduler_decisions WHERE workflow_id=? AND task_id=? ORDER BY id DESC LIMIT 1;

-- name: GetLatestArtifact :one
SELECT * FROM workflow_artifacts WHERE attempt_id=? AND repo_key=? ORDER BY artifact_revision DESC LIMIT 1;

-- name: StalePreviousArtifacts :exec
UPDATE workflow_artifacts SET state='stale',revision=revision+1
WHERE attempt_id=? AND repo_key=? AND state<>'stale';

-- name: TransitionArtifact :execrows
UPDATE workflow_artifacts SET state=sqlc.arg(next_state),revision=revision+1
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(expected_revision)
    AND fingerprint=sqlc.arg(fingerprint) AND state=sqlc.arg(expected_state);

-- name: InsertPathReservation :exec
INSERT INTO workflow_task_path_reservations(admission_id,attempt_id,workflow_id,task_id,repo_key,
    project_id,repo_path,repo_identity,scope_path,owner_id,epoch,lease_until)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?);

-- name: ListOccupiedPathReservations :many
SELECT * FROM workflow_task_path_reservations WHERE released_at IS NULL ORDER BY id;

-- name: ListAttemptPathReservations :many
SELECT * FROM workflow_task_path_reservations WHERE attempt_id=? ORDER BY id;

-- name: BindPathReservation :execrows
UPDATE workflow_task_path_reservations SET session_id=sqlc.arg(session_id),generation=sqlc.arg(generation),revision=revision+1
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(revision) AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch)
    AND released_at IS NULL AND (session_id='' OR session_id=sqlc.arg(session_id));

-- name: RenewPathReservations :exec
UPDATE workflow_task_path_reservations SET lease_until=sqlc.arg(lease_until),revision=revision+1
WHERE attempt_id=sqlc.arg(attempt_id) AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch)
    AND session_id=sqlc.arg(session_id) AND generation=sqlc.arg(generation) AND released_at IS NULL;

-- name: AdoptPathReservations :exec
UPDATE workflow_task_path_reservations SET owner_id=sqlc.arg(owner_id),epoch=sqlc.arg(epoch),revision=revision+1
WHERE released_at IS NULL AND (owner_id<>sqlc.arg(owner_id) OR epoch<>sqlc.arg(epoch))
    AND (CAST(sqlc.arg(admission_id) AS TEXT)='' OR admission_id=CAST(sqlc.arg(admission_id) AS TEXT));

-- name: ReleasePathReservation :execrows
UPDATE workflow_task_path_reservations SET released_at=sqlc.arg(now),revision=revision+1
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(revision) AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch)
    AND session_id=sqlc.arg(session_id) AND generation=sqlc.arg(generation) AND released_at IS NULL;
