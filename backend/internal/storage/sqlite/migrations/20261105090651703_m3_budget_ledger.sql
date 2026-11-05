-- +goose Up
ALTER TABLE workflow_usage_attributions ADD COLUMN billing_provider_id TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_usage_attributions ADD COLUMN billing_provider_source TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_usage_attributions ADD COLUMN measurement_kind TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE workflow_usage_attributions ADD COLUMN updated_at TIMESTAMP NOT NULL DEFAULT '1970-01-01 00:00:00+00:00';
ALTER TABLE workflow_usage_corrections ADD COLUMN previous_snapshot_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE workflow_usage_corrections ADD COLUMN new_snapshot_json TEXT NOT NULL DEFAULT '{}';

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

CREATE TABLE budget_soft_alerts (
 policy_id TEXT NOT NULL REFERENCES budget_policies(id),window TEXT NOT NULL,
 policy_revision INTEGER NOT NULL CHECK(policy_revision>0),threshold INTEGER NOT NULL CHECK(threshold IN (80,95)),
 created_at TIMESTAMP NOT NULL,PRIMARY KEY(policy_id,window,policy_revision,threshold)
);
-- +goose StatementBegin
CREATE TRIGGER budget_usage_created AFTER INSERT ON workflow_usage_attributions BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','usage','id',NEW.event_id,'revision',NEW.revision));
END;
CREATE TRIGGER budget_usage_corrected AFTER UPDATE ON workflow_usage_attributions BEGIN
 INSERT INTO workflow_usage_corrections(correction_key,event_id,previous_revision,revision,old_cost_nanos,new_cost_nanos,reason_code,created_at,previous_snapshot_json,new_snapshot_json)
 VALUES(printf('%d:%d',NEW.event_id,NEW.revision),NEW.event_id,OLD.revision,NEW.revision,OLD.cost_nanos,NEW.cost_nanos,'USAGE_RECONCILED',NEW.updated_at,
 json_object('sessionId',OLD.session_id,'attemptId',OLD.attempt_id,'workflowId',OLD.workflow_id,'taskId',OLD.task_id,'occurredAt',OLD.occurred_at,'costNanos',OLD.cost_nanos,'quality',OLD.quality,'priceVersion',OLD.price_version,'billingProviderId',OLD.billing_provider_id,'billingProviderSource',OLD.billing_provider_source,'measurementKind',OLD.measurement_kind),
 json_object('sessionId',NEW.session_id,'attemptId',NEW.attempt_id,'workflowId',NEW.workflow_id,'taskId',NEW.task_id,'occurredAt',NEW.occurred_at,'costNanos',NEW.cost_nanos,'quality',NEW.quality,'priceVersion',NEW.price_version,'billingProviderId',NEW.billing_provider_id,'billingProviderSource',NEW.billing_provider_source,'measurementKind',NEW.measurement_kind));
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','usage','id',NEW.event_id,'revision',NEW.revision));
END;
CREATE TRIGGER budget_soft_alert_created AFTER INSERT ON budget_soft_alerts BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','budget_alert','id',NEW.policy_id,'state',CAST(NEW.threshold AS TEXT),'revision',NEW.policy_revision));
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE budget_soft_alerts;
DROP TRIGGER budget_usage_corrected;
DROP TRIGGER budget_usage_created;
DROP VIEW budget_usage_pending;
DROP VIEW budget_usage_facts;
ALTER TABLE workflow_usage_corrections DROP COLUMN new_snapshot_json;
ALTER TABLE workflow_usage_corrections DROP COLUMN previous_snapshot_json;
ALTER TABLE workflow_usage_attributions DROP COLUMN updated_at;
ALTER TABLE workflow_usage_attributions DROP COLUMN measurement_kind;
ALTER TABLE workflow_usage_attributions DROP COLUMN billing_provider_source;
ALTER TABLE workflow_usage_attributions DROP COLUMN billing_provider_id;
