-- name: ListBudgetReconcileCandidates :many
SELECT * FROM budget_usage_pending ORDER BY event_id LIMIT sqlc.arg(row_limit);

-- name: UpsertBudgetUsageAttribution :exec
INSERT INTO workflow_usage_attributions(event_id,session_id,attempt_id,workflow_id,task_id,occurred_at,currency,cost_nanos,quality,revision,price_version,billing_provider_id,billing_provider_source,measurement_kind,updated_at)
VALUES(sqlc.arg(event_id),sqlc.arg(session_id),sqlc.arg(attempt_id),sqlc.arg(workflow_id),sqlc.arg(task_id),sqlc.narg(occurred_at),sqlc.arg(currency),sqlc.narg(cost_nanos),sqlc.arg(quality),1,sqlc.arg(price_version),sqlc.arg(billing_provider_id),sqlc.arg(billing_provider_source),sqlc.arg(measurement_kind),sqlc.arg(now))
ON CONFLICT(event_id) DO UPDATE SET session_id=excluded.session_id,attempt_id=excluded.attempt_id,workflow_id=excluded.workflow_id,task_id=excluded.task_id,
 occurred_at=excluded.occurred_at,currency=excluded.currency,cost_nanos=excluded.cost_nanos,quality=excluded.quality,revision=workflow_usage_attributions.revision+1,
 price_version=excluded.price_version,billing_provider_id=excluded.billing_provider_id,billing_provider_source=excluded.billing_provider_source,measurement_kind=excluded.measurement_kind,updated_at=excluded.updated_at;

-- name: ListBudgetUsagePage :many
WITH RECURSIVE budget_tasks(task_id) AS (
 SELECT CAST(sqlc.arg(scope_id) AS TEXT) AS task_id
 UNION SELECT m.child_task_id FROM workflow_task_materializations m JOIN budget_tasks t ON m.parent_task_id=t.task_id
)
SELECT a.*,e.binding_id,e.source_event_key,e.input_tokens,e.cached_input_tokens,e.uncached_input_tokens,e.output_tokens
FROM workflow_usage_attributions a JOIN model_usage_events e ON e.id=a.event_id
WHERE (sqlc.arg(scope)='global' OR (sqlc.arg(scope)='workflow' AND a.workflow_id=sqlc.arg(scope_id)) OR (sqlc.arg(scope)='task' AND a.task_id IN (SELECT task_id FROM budget_tasks)))
AND a.event_id>sqlc.arg(after_id) ORDER BY a.event_id LIMIT sqlc.arg(row_limit);

-- name: ListBudgetUsageCorrections :many
SELECT * FROM workflow_usage_corrections WHERE event_id=? ORDER BY revision;

-- name: RecordBudgetSoftAlert :execrows
INSERT OR IGNORE INTO budget_soft_alerts(policy_id,window,policy_revision,threshold,created_at)
SELECT sqlc.arg(policy_id),sqlc.arg(window),sqlc.arg(policy_revision),sqlc.arg(threshold),sqlc.arg(now)
WHERE EXISTS(SELECT 1 FROM budget_policies WHERE id=sqlc.arg(policy_id) AND revision=sqlc.arg(policy_revision) AND mode='soft');

-- name: MarkReleasedBudgetReservationsUncertain :exec
UPDATE budget_reservations SET state='uncertain',revision=revision+1
WHERE state='reserved' AND admission_id IN (
 SELECT a.id FROM scheduler_admissions a
 WHERE a.state='released' AND EXISTS(SELECT 1 FROM budget_reservations r WHERE r.admission_id=a.id AND r.state='reserved')
 ORDER BY a.id LIMIT sqlc.arg(row_limit)
);

-- name: ListSettleableBudgetAdmissions :many
SELECT a.id FROM scheduler_admissions a JOIN sessions s ON s.id=a.session_id
WHERE a.state='released' AND s.is_terminated=1
AND a.controller_generation=CASE WHEN s.session_mode='chat' THEN s.controller_generation ELSE s.runtime_launch_id END
AND EXISTS(SELECT 1 FROM budget_reservations r WHERE r.admission_id=a.id AND r.state IN ('reserved','uncertain'))
AND EXISTS(SELECT 1 FROM usage_bindings b WHERE b.session_id=s.id)
AND NOT EXISTS(SELECT 1 FROM usage_bindings b WHERE b.session_id=s.id
 AND (b.state<>'complete' OR b.last_error_code<>'' OR b.harness NOT IN ('codex','claude-code','kimi')
 OR NOT EXISTS(SELECT 1 FROM usage_sources u WHERE u.binding_id=b.id)
 OR EXISTS(SELECT 1 FROM usage_sources u WHERE u.binding_id=b.id AND (u.state<>'complete' OR (u.last_error_code<>'' AND u.last_error_code<>'artifact_replaced')))))
AND NOT EXISTS(SELECT 1 FROM budget_usage_pending p WHERE p.session_id=s.id)
AND NOT EXISTS(SELECT 1 FROM workflow_usage_attributions u WHERE u.session_id=s.id
 AND (u.quality='UNKNOWN' OR u.cost_nanos IS NULL OR u.occurred_at IS NULL OR u.currency<>'USD'))
ORDER BY a.id LIMIT sqlc.arg(row_limit);

-- name: ConsumeReleasedBudgetReservations :exec
UPDATE budget_reservations SET state='consumed',revision=revision+1
WHERE admission_id=? AND state IN ('reserved','uncertain');
