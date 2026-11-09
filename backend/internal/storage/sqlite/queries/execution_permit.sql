-- name: AcceptSessionAdmission :execrows
UPDATE scheduler_admissions SET session_id=sqlc.arg(session_id),state='active',revision=revision+1,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(revision) AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch)
AND state='reserved' AND session_id IS NULL;

-- name: RenewExecutionAttemptLease :execrows
UPDATE workflow_attempts SET heartbeat_at=sqlc.arg(now),lease_until=sqlc.arg(lease_until),updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND worker_id=sqlc.arg(worker_id) AND lease_token=sqlc.arg(lease_token)
AND state IN ('claimed','running') AND lease_until=sqlc.arg(expected_lease_until) AND lease_until>sqlc.arg(now)
AND updated_at=sqlc.arg(expected_updated_at);

-- name: ReleaseExecutionAdmission :execrows
UPDATE scheduler_admissions SET state='released',revision=revision+1,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(revision) AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch)
AND session_id=sqlc.arg(session_id) AND state<>'released';

-- name: FreezeExecutionBudgetReservations :exec
UPDATE budget_reservations SET state='uncertain',revision=revision+1
WHERE admission_id=sqlc.arg(admission_id) AND state='reserved';

-- name: GetExecutionSessionNumFloor :one
SELECT CAST(COALESCE((SELECT max_num FROM session_num_floors WHERE id_prefix=?),0) AS INTEGER) AS max_num;

-- name: RecoverExecutionAdmissionSession :execrows
UPDATE scheduler_admissions SET session_id=sqlc.arg(session_id),state=sqlc.arg(state),revision=revision+1,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(revision) AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch)
AND state<>'released' AND (session_id IS NULL OR session_id=sqlc.arg(session_id));

-- name: HasExecutionSettlementReplay :one
SELECT EXISTS(SELECT 1 FROM workflow_idempotency
WHERE workflow_id=sqlc.arg(workflow_id) AND operation='attempt.settle'
AND request_fingerprint=sqlc.arg(request_fingerprint) AND result_type='attempt' AND result_id=sqlc.arg(attempt_id));

-- name: GetOccupiedSessionAdmission :one
SELECT * FROM scheduler_admissions WHERE session_id=? AND state<>'released';

-- name: AcceptSessionRestart :execrows
UPDATE scheduler_admissions SET controller_generation=sqlc.arg(generation),revision=revision+1,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(revision) AND state='active'
AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch);

-- An already-terminated session has no unpublished controller, so a slot still
-- naming another generation is provably stale; this is the only path allowed to
-- revive an uncertain slot, and it never applies to a live session.
-- name: AcceptStaleSessionRestart :execrows
UPDATE scheduler_admissions SET controller_generation=sqlc.arg(generation),state='active',revision=revision+1,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(revision) AND state IN ('active','uncertain')
AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch);

-- name: ReleaseDeletedSessionAdmissions :exec
UPDATE scheduler_admissions SET state='released',revision=revision+1,updated_at=CURRENT_TIMESTAMP
WHERE session_id=? AND state<>'released';

-- name: FreezeDeletedSessionBudgets :exec
UPDATE budget_reservations SET state='uncertain',revision=revision+1
WHERE state='reserved' AND admission_id IN (SELECT id FROM scheduler_admissions WHERE session_id=?);

-- name: CountSessionAdmissionHistory :one
SELECT count(*) FROM scheduler_admissions WHERE session_id=?;

-- name: ReattachSessionAdmissionGeneration :execrows
UPDATE scheduler_admissions SET controller_generation=sqlc.arg(generation),state='active',revision=revision+1
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(revision) AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch)
AND state IN ('active','uncertain');

-- name: ReattachWorkflowBindingGeneration :execrows
UPDATE workflow_attempt_bindings SET controller_generation=sqlc.arg(generation),revision=revision+1
WHERE attempt_id=sqlc.arg(attempt_id) AND session_id=sqlc.arg(session_id)
AND controller_generation=sqlc.arg(previous_generation) AND revision=sqlc.arg(revision);
