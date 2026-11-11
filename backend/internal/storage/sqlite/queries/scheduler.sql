-- name: GetSchedulerLeader :one
SELECT * FROM scheduler_leader WHERE id=1;

-- name: LockSchedulerWriter :exec
UPDATE scheduler_leader SET revision=revision WHERE id=1;

-- name: GetSchedulerPolicyChange :one
SELECT * FROM scheduler_policy_changes WHERE idempotency_key=?;

-- name: InsertSchedulerPolicyChange :exec
INSERT INTO scheduler_policy_changes(idempotency_key,request_fingerprint,kind,policy_id,result_json,actor_ref,reason,created_at) VALUES(?,?,?,?,?,?,?,?);

-- name: AcquireSchedulerLeader :execrows
UPDATE scheduler_leader SET owner_id=sqlc.arg(owner_id),epoch=epoch+1,lease_until=sqlc.arg(lease_until),revision=revision+1,recovered=0
WHERE id=1 AND lease_until<=sqlc.arg(now) AND epoch<9223372036854775807 AND revision<9223372036854775807;

-- name: RenewSchedulerLeader :execrows
UPDATE scheduler_leader SET lease_until=sqlc.arg(lease_until),revision=revision+1
WHERE id=1 AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch) AND revision=sqlc.arg(revision) AND lease_until>sqlc.arg(now) AND revision<9223372036854775807;

-- name: CompleteSchedulerRecovery :execrows
UPDATE scheduler_leader SET recovered=1,revision=revision+1
WHERE id=1 AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch) AND revision=sqlc.arg(revision) AND lease_until>sqlc.arg(now) AND recovered=0;

-- name: SetSchedulerCursor :execrows
UPDATE scheduler_leader SET cursor=sqlc.arg(cursor),revision=revision+1
WHERE id=1 AND owner_id=sqlc.arg(owner_id) AND epoch=sqlc.arg(epoch) AND revision=sqlc.arg(revision) AND lease_until>sqlc.arg(now) AND recovered=1;

-- name: ListSchedulerPolicies :many
SELECT * FROM scheduler_policies ORDER BY id;

-- name: PutSchedulerPolicy :execrows
INSERT INTO scheduler_policies(id,scope,scope_id,max_concurrent,policy_json,revision)
SELECT sqlc.arg(id),sqlc.arg(scope),sqlc.arg(scope_id),sqlc.arg(max_concurrent),sqlc.arg(policy_json),sqlc.arg(next_revision)
WHERE sqlc.arg(expected_revision)=0 OR EXISTS(SELECT 1 FROM scheduler_policies WHERE id=sqlc.arg(id) AND revision=sqlc.arg(expected_revision))
ON CONFLICT(id) DO UPDATE SET max_concurrent=excluded.max_concurrent,policy_json=excluded.policy_json,revision=excluded.revision
WHERE scheduler_policies.revision=excluded.revision-1 AND scheduler_policies.scope=excluded.scope AND scheduler_policies.scope_id=excluded.scope_id;

-- name: ListBudgetPolicies :many
SELECT * FROM budget_policies ORDER BY id;

-- name: PutBudgetPolicy :execrows
INSERT INTO budget_policies(id,scope,scope_id,currency,mode,period,limit_nanos,reservation_nanos,reservation_basis,reservation_ref,revision)
SELECT sqlc.arg(id),sqlc.arg(scope),sqlc.arg(scope_id),sqlc.arg(currency),sqlc.arg(mode),sqlc.arg(period),sqlc.arg(limit_nanos),sqlc.narg(reservation_nanos),sqlc.arg(reservation_basis),sqlc.arg(reservation_ref),sqlc.arg(next_revision)
WHERE sqlc.arg(expected_revision)=0 OR EXISTS(SELECT 1 FROM budget_policies WHERE id=sqlc.arg(id) AND revision=sqlc.arg(expected_revision))
ON CONFLICT(id) DO UPDATE SET mode=excluded.mode,limit_nanos=excluded.limit_nanos,reservation_nanos=excluded.reservation_nanos,reservation_basis=excluded.reservation_basis,reservation_ref=excluded.reservation_ref,revision=excluded.revision
WHERE budget_policies.revision=excluded.revision-1 AND budget_policies.scope=excluded.scope AND budget_policies.scope_id=excluded.scope_id AND budget_policies.currency=excluded.currency AND budget_policies.period=excluded.period;

-- name: InsertSchedulerAdmission :exec
INSERT INTO scheduler_admissions(id,attempt_id,workflow_id,task_id,session_id,project_id,harness,mode,client_request_id,request_fingerprint,owner_id,epoch,state,revision,created_at,updated_at,controller_generation)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?);

-- name: GetSchedulerAdmission :one
SELECT * FROM scheduler_admissions WHERE id=?;

-- name: GetSchedulerAdmissionByAttempt :one
SELECT * FROM scheduler_admissions WHERE attempt_id=?;

-- name: GetSchedulerAdmissionByRequest :one
SELECT * FROM scheduler_admissions WHERE client_request_id=?;

-- name: ListSchedulerAdmissions :many
SELECT * FROM scheduler_admissions WHERE sqlc.arg(occupied_only)=0 OR state<>'released' ORDER BY created_at,id;

-- name: AdoptSchedulerAdmission :execrows
UPDATE scheduler_admissions SET owner_id=sqlc.arg(owner_id),epoch=sqlc.arg(epoch),state='uncertain',revision=revision+1,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(revision) AND state<>'released' AND epoch<=sqlc.arg(epoch);

-- name: ListSchedulerOccupancy :many
SELECT a.harness FROM scheduler_admissions a
LEFT JOIN sessions s ON s.id=a.session_id
WHERE a.state<>'released'
AND NOT (a.attempt_id IS NULL AND a.mode='chat' AND a.state='active' AND s.id IS NOT NULL AND s.is_terminated=0 AND s.activity_state='exited')
UNION ALL
SELECT s.harness FROM sessions s WHERE s.is_terminated=0 AND s.is_task_preparation=0
AND s.id<>sqlc.arg(replacing_session_id)
AND NOT EXISTS(SELECT 1 FROM scheduler_admissions a LEFT JOIN workflow_attempt_bindings b ON b.attempt_id=a.attempt_id
WHERE a.state<>'released' AND (a.session_id=s.id OR (a.client_request_id IS NOT NULL AND a.client_request_id=s.client_request_id) OR b.session_id=s.id))
UNION ALL
SELECT t.agent_harness FROM workflow_attempts old JOIN workflow_tasks t ON t.workflow_id=old.workflow_id AND t.id=old.task_id
LEFT JOIN workflow_attempt_bindings b ON b.attempt_id=old.id
WHERE old.state IN ('claimed','running','settling','unknown')
AND NOT EXISTS(SELECT 1 FROM scheduler_admissions a WHERE a.attempt_id=old.id)
AND NOT EXISTS(SELECT 1 FROM sessions s WHERE s.is_terminated=0 AND s.is_task_preparation=0 AND (s.id=old.session_id OR s.id=b.session_id));

-- name: InsertBudgetReservation :exec
INSERT INTO budget_reservations(admission_id,attempt_id,policy_id,window,amount_nanos,basis,reference,state,revision) VALUES(?,?,?,?,?,?,?,'reserved',1);

-- name: ListBudgetReservations :many
SELECT * FROM budget_reservations WHERE sqlc.arg(admission_id)='' OR admission_id=sqlc.arg(admission_id);

-- name: ListBudgetOutstanding :many
SELECT amount_nanos FROM budget_reservations WHERE policy_id=? AND window=? AND state IN ('reserved','uncertain');

-- name: ListBudgetUsage :many
WITH RECURSIVE budget_tasks(task_id) AS (
 SELECT CAST(sqlc.arg(scope_id) AS TEXT)
 UNION SELECT m.child_task_id FROM workflow_task_materializations m JOIN budget_tasks t ON m.parent_task_id=t.task_id
)
SELECT u.cost_nanos,u.currency,u.quality,u.occurred_at FROM workflow_usage_attributions u
WHERE (sqlc.arg(scope)='global' OR (sqlc.arg(scope)='workflow' AND u.workflow_id=sqlc.arg(scope_id)) OR (sqlc.arg(scope)='task' AND u.task_id IN (SELECT task_id FROM budget_tasks)));

-- name: CountUnattributedBudgetUsage :one
SELECT count(*) FROM budget_usage_pending;

-- name: CountUnreservedBudgetAdmissions :one
WITH RECURSIVE budget_tasks(task_id) AS (
 SELECT CAST(sqlc.arg(scope_id) AS TEXT)
 UNION SELECT m.child_task_id FROM workflow_task_materializations m JOIN budget_tasks t ON m.parent_task_id=t.task_id
)
SELECT count(*) FROM scheduler_admissions a
WHERE a.state<>'released' AND (sqlc.arg(scope)='global' OR (sqlc.arg(scope)='workflow' AND a.workflow_id=sqlc.arg(scope_id)) OR (sqlc.arg(scope)='task' AND a.task_id IN (SELECT task_id FROM budget_tasks)))
AND NOT EXISTS(SELECT 1 FROM budget_reservations r WHERE r.admission_id=a.id AND r.policy_id=sqlc.arg(policy_id));

-- name: CountLegacyBudgetOccupancy :one
WITH RECURSIVE budget_tasks(task_id) AS (
 SELECT CAST(sqlc.arg(scope_id) AS TEXT)
 UNION SELECT m.child_task_id FROM workflow_task_materializations m JOIN budget_tasks t ON m.parent_task_id=t.task_id
)
SELECT
(SELECT count(*) FROM workflow_attempts old WHERE old.state IN ('claimed','running','settling','unknown')
AND (sqlc.arg(scope)='global' OR (sqlc.arg(scope)='workflow' AND old.workflow_id=sqlc.arg(scope_id)) OR (sqlc.arg(scope)='task' AND old.task_id IN (SELECT task_id FROM budget_tasks)))
AND NOT EXISTS(SELECT 1 FROM scheduler_admissions a WHERE a.attempt_id=old.id))
+ (SELECT count(*) FROM sessions s WHERE sqlc.arg(scope)='global' AND s.is_terminated=0 AND s.is_task_preparation=0
AND NOT EXISTS(SELECT 1 FROM scheduler_admissions a LEFT JOIN workflow_attempt_bindings b ON b.attempt_id=a.attempt_id WHERE a.session_id=s.id OR a.client_request_id=s.client_request_id OR b.session_id=s.id)) AS total;

-- name: InsertSchedulerDecision :exec
INSERT INTO scheduler_decisions(admission_id,workflow_id,task_id,owner_id,epoch,reason_code,policy_revisions_json,created_at) VALUES(?,?,?,?,?,?,?,?);

-- name: ListSchedulerDecisions :many
SELECT * FROM scheduler_decisions WHERE id>? ORDER BY id LIMIT ?;

-- name: PutSchedulerWorkflowState :execrows
INSERT INTO scheduler_workflow_state(workflow_id,deficit,candidate_offset,weight,paused,revision)
SELECT sqlc.arg(workflow_id),sqlc.arg(deficit),sqlc.arg(candidate_offset),sqlc.arg(weight),sqlc.arg(paused),sqlc.arg(next_revision)
WHERE sqlc.arg(expected_revision)=0 OR EXISTS(SELECT 1 FROM scheduler_workflow_state WHERE workflow_id=sqlc.arg(workflow_id) AND revision=sqlc.arg(expected_revision))
ON CONFLICT(workflow_id) DO UPDATE SET deficit=excluded.deficit,candidate_offset=excluded.candidate_offset,weight=excluded.weight,paused=excluded.paused,revision=excluded.revision WHERE scheduler_workflow_state.revision=excluded.revision-1;

-- name: ListSchedulerWorkflowStates :many
SELECT * FROM scheduler_workflow_state ORDER BY workflow_id;

-- name: PutSchedulerRetryDue :exec
INSERT INTO scheduler_retry_due(workflow_id,task_id,attempt_id,harness,next_eligible_at,reason_code,policy_revision) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(workflow_id,task_id) DO UPDATE SET attempt_id=excluded.attempt_id,harness=excluded.harness,next_eligible_at=MAX(scheduler_retry_due.next_eligible_at,excluded.next_eligible_at),reason_code=excluded.reason_code,policy_revision=excluded.policy_revision;

-- name: ListSchedulerRetryDue :many
SELECT * FROM scheduler_retry_due ORDER BY next_eligible_at,workflow_id,task_id;

-- name: ListSchedulerWorkflows :many
SELECT * FROM workflows WHERE state='active' AND id>sqlc.arg(after_id) ORDER BY id LIMIT sqlc.arg(row_limit);

-- name: ListSchedulerCandidates :many
WITH clock AS (SELECT CAST(sqlc.arg(now_epoch) AS INTEGER) AS seconds)
SELECT workflow_tasks.* FROM workflow_tasks,clock WHERE workflow_tasks.workflow_id=sqlc.arg(workflow_id) AND workflow_tasks.state='ready'
AND (workflow_tasks.planner=TRUE OR NOT EXISTS (
 SELECT 1 FROM workflow_tasks planner WHERE planner.workflow_id=workflow_tasks.workflow_id
 AND planner.planner=TRUE AND planner.state NOT IN ('succeeded','cancelled','failed')
))
ORDER BY CASE WHEN workflow_tasks.priority > 9223372036854775807 - MIN(5,MAX(0,(clock.seconds-unixepoch(substr(workflow_tasks.updated_at,1,19)))/300)) THEN 9223372036854775807
ELSE workflow_tasks.priority + MIN(5,MAX(0,(clock.seconds-unixepoch(substr(workflow_tasks.updated_at,1,19)))/300)) END DESC,
workflow_tasks.created_at,workflow_tasks.id LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(candidate_offset);
