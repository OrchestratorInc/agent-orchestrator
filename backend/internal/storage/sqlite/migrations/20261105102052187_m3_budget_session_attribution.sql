-- +goose Up
-- 普通 Session 可有多次准入；事件归属只取唯一 Workflow Binding，避免账本 JOIN 放大。
DROP VIEW budget_usage_pending;
DROP VIEW budget_usage_facts;
CREATE VIEW budget_usage_facts AS
SELECT e.id AS event_id,b.session_id,COALESCE(w.attempt_id,'') AS attempt_id,
 COALESCE(w.workflow_id,'') AS workflow_id,COALESCE(w.task_id,'') AS task_id,
 e.created_at AS occurred_at,'USD' AS currency,e.estimated_cost_nanos AS cost_nanos,
 CASE WHEN e.estimated_cost_nanos IS NULL OR e.created_at IS NULL
 OR b.harness NOT IN ('codex','claude-code','kimi')
 OR e.usage_measurement_kind NOT IN ('native_reported','ao_estimated','mixed')
 OR COALESCE(e.billing_provider_id,'')='' OR e.pricing_version=''
 OR COALESCE(e.billing_provider_source,'') NOT IN ('observed','inferred')
 THEN 'UNKNOWN' ELSE 'ESTIMATED' END AS quality,
 e.pricing_version AS price_version,COALESCE(e.billing_provider_id,'') AS billing_provider_id,
 COALESCE(e.billing_provider_source,'') AS billing_provider_source,e.usage_measurement_kind AS measurement_kind
FROM model_usage_events e JOIN usage_bindings b ON b.id=e.binding_id
LEFT JOIN workflow_attempt_bindings w ON w.session_id=b.session_id;

CREATE VIEW budget_usage_pending AS
SELECT f.*,a.revision AS previous_revision FROM budget_usage_facts f
LEFT JOIN workflow_usage_attributions a ON a.event_id=f.event_id
WHERE a.event_id IS NULL OR a.session_id<>f.session_id OR a.attempt_id<>f.attempt_id
OR a.workflow_id<>f.workflow_id OR a.task_id<>f.task_id OR a.occurred_at IS NOT f.occurred_at
OR a.cost_nanos IS NOT f.cost_nanos OR a.currency<>f.currency OR a.quality<>f.quality
OR a.price_version<>f.price_version OR a.billing_provider_id<>f.billing_provider_id
OR a.billing_provider_source<>f.billing_provider_source OR a.measurement_kind<>f.measurement_kind;


-- +goose Down
DROP VIEW budget_usage_pending;
DROP VIEW budget_usage_facts;
CREATE VIEW budget_usage_facts AS
SELECT e.id AS event_id,b.session_id,COALESCE(w.attempt_id,a.attempt_id,'') AS attempt_id,
 COALESCE(w.workflow_id,a.workflow_id,'') AS workflow_id,COALESCE(w.task_id,a.task_id,'') AS task_id,
 e.created_at AS occurred_at,'USD' AS currency,e.estimated_cost_nanos AS cost_nanos,
 CASE WHEN e.estimated_cost_nanos IS NULL OR e.created_at IS NULL
 OR b.harness NOT IN ('codex','claude-code','kimi')
 OR e.usage_measurement_kind NOT IN ('native_reported','ao_estimated','mixed')
 OR COALESCE(e.billing_provider_id,'')='' OR e.pricing_version=''
 OR COALESCE(e.billing_provider_source,'') NOT IN ('observed','inferred')
 THEN 'UNKNOWN' ELSE 'ESTIMATED' END AS quality,
 e.pricing_version AS price_version,COALESCE(e.billing_provider_id,'') AS billing_provider_id,
 COALESCE(e.billing_provider_source,'') AS billing_provider_source,e.usage_measurement_kind AS measurement_kind
FROM model_usage_events e JOIN usage_bindings b ON b.id=e.binding_id
LEFT JOIN workflow_attempt_bindings w ON w.session_id=b.session_id
LEFT JOIN scheduler_admissions a ON a.session_id=b.session_id AND w.attempt_id IS NULL;

CREATE VIEW budget_usage_pending AS
SELECT f.*,a.revision AS previous_revision FROM budget_usage_facts f
LEFT JOIN workflow_usage_attributions a ON a.event_id=f.event_id
WHERE a.event_id IS NULL OR a.session_id<>f.session_id OR a.attempt_id<>f.attempt_id
OR a.workflow_id<>f.workflow_id OR a.task_id<>f.task_id OR a.occurred_at IS NOT f.occurred_at
OR a.cost_nanos IS NOT f.cost_nanos OR a.currency<>f.currency OR a.quality<>f.quality
OR a.price_version<>f.price_version OR a.billing_provider_id<>f.billing_provider_id
OR a.billing_provider_source<>f.billing_provider_source OR a.measurement_kind<>f.measurement_kind;
