-- name: InsertWorkflowPlanCandidate :exec
INSERT INTO workflow_plan_candidates(id,workflow_id,source_attempt_id,source_turn_id,stop_intervention_id,expected_revision,digest,definition_json,idempotency_key,request_fingerprint,state_fingerprint,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?);

-- name: GetWorkflowPlanCandidate :one
SELECT * FROM workflow_plan_candidates WHERE workflow_id=? AND id=?;

-- name: FindWorkflowPlanCandidate :one
SELECT * FROM workflow_plan_candidates WHERE idempotency_key=?;

-- name: ListWorkflowPlanCandidates :many
SELECT * FROM workflow_plan_candidates WHERE workflow_id=? ORDER BY created_at DESC LIMIT 100;

-- name: ConfirmWorkflowPlanCandidate :execrows
UPDATE workflow_plan_candidates SET confirmed_revision=? WHERE id=? AND confirmed_revision=0;

-- name: FinishPlanningAttempt :execrows
UPDATE workflow_attempts SET state='succeeded',lease_token='',lease_until=NULL,ended_at=sqlc.arg(now),updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND state IN ('claimed','running') AND lease_until>sqlc.arg(now);

-- name: PauseWorkflowForPlan :execrows
UPDATE workflows SET revision=revision+1,updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(workflow_id) AND revision=sqlc.arg(expected_revision) AND state IN ('active','paused');

-- name: PausePlanScheduling :exec
INSERT INTO scheduler_workflow_state(workflow_id,deficit,candidate_offset,weight,paused,revision)
VALUES (?,0,0,1,TRUE,1)
ON CONFLICT(workflow_id) DO UPDATE SET paused=TRUE,revision=scheduler_workflow_state.revision+1;

-- name: WorkflowPlanAttemptFacts :many
SELECT a.id,a.task_id,a.state,a.session_id,COALESCE(b.revision,0) AS binding_revision
FROM workflow_attempts a LEFT JOIN workflow_attempt_bindings b ON b.attempt_id=a.id
WHERE a.workflow_id=? ORDER BY a.id;

-- name: HasNewerPlannerTurn :one
SELECT EXISTS(SELECT 1 FROM conversation_turns WHERE handled_by_session_id=sqlc.arg(session_id)
AND (requested_at>sqlc.arg(requested_at) OR (requested_at=sqlc.arg(requested_at) AND id<>sqlc.arg(turn_id))));

-- name: ListWorkflowPlanRetirements :many
WITH retiring(attempt_id) AS (
 SELECT source_attempt_id FROM workflow_plan_candidates WHERE confirmed_revision>0
 UNION
 SELECT stopped_attempt_id FROM workflow_replans
 UNION
 SELECT i.attempt_id FROM workflow_plan_candidates p JOIN workflow_interventions i ON i.id=p.stop_intervention_id
 WHERE p.confirmed_revision>0 AND i.state='stop_confirmed'
)
SELECT a.id AS attempt_id,b.session_id,b.controller_generation,
 (SELECT COALESCE(MAX(artifact_revision),0)+1 FROM workflow_artifacts WHERE attempt_id=a.id) AS artifact_revision
FROM retiring r JOIN workflow_attempts a ON a.id=r.attempt_id
JOIN workflow_attempt_bindings b ON b.attempt_id=a.id
JOIN sessions s ON s.id=b.session_id
WHERE a.state IN ('succeeded','failed') AND (
 s.is_terminated=0
 OR EXISTS(SELECT 1 FROM scheduler_admissions ad WHERE ad.attempt_id=a.id AND ad.state<>'released')
 OR EXISTS(SELECT 1 FROM workflow_task_path_reservations pr WHERE pr.attempt_id=a.id AND pr.released_at IS NULL)
)
ORDER BY a.id LIMIT 100;

-- name: InsertWorkflowReplan :exec
INSERT INTO workflow_replans(planner_task_id,workflow_id,stop_intervention_id,stopped_attempt_id,idempotency_key,request_fingerprint)
VALUES(?,?,?,?,?,?);

-- name: GetWorkflowReplan :one
SELECT * FROM workflow_replans WHERE planner_task_id=?;

-- name: FindWorkflowReplan :one
SELECT * FROM workflow_replans WHERE idempotency_key=?;

-- name: ResumeReplanScheduling :exec
INSERT INTO scheduler_workflow_state(workflow_id,deficit,candidate_offset,weight,paused,revision)
VALUES (?,0,0,1,FALSE,1)
ON CONFLICT(workflow_id) DO UPDATE SET paused=FALSE,revision=scheduler_workflow_state.revision+1;

-- name: CancelStoppedPlanAttempt :execrows
UPDATE workflow_attempts SET state='failed',lease_token='',lease_until=NULL,ended_at=sqlc.arg(now),updated_at=sqlc.arg(now),error_code='operator_replanned'
WHERE id=sqlc.arg(id) AND state IN ('claimed','running') AND lease_until>sqlc.arg(now);
