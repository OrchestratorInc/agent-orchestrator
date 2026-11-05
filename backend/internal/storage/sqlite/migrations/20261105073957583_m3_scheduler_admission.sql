-- +goose NO TRANSACTION
-- +goose Up
-- 沿用 Workflow CDC 扩展白名单；表与触发器在显式事务内整体建立。
-- +goose StatementBegin
PRAGMA writable_schema = ON;
UPDATE sqlite_master SET sql = replace(sql, '''workflow_attempt_updated'',', '''workflow_attempt_updated'', ''scheduler_updated'',')
WHERE type = 'table' AND name = 'change_log' AND instr(sql, '''scheduler_updated''')=0;
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE scheduler_cdc_guard (ok INTEGER CHECK(ok=1));
INSERT INTO scheduler_cdc_guard SELECT count(*) FROM sqlite_master WHERE name='change_log' AND instr(sql, '''scheduler_updated''')>0;
DROP TABLE scheduler_cdc_guard;
-- +goose StatementEnd
-- +goose StatementBegin
BEGIN IMMEDIATE;
CREATE TABLE scheduler_leader (
 id INTEGER PRIMARY KEY CHECK(id=1), owner_id TEXT NOT NULL, epoch INTEGER NOT NULL CHECK(epoch>=0),
 lease_until TIMESTAMP NOT NULL, revision INTEGER NOT NULL CHECK(revision>0),
 recovered INTEGER NOT NULL CHECK(recovered IN (0,1)), cursor TEXT NOT NULL DEFAULT ''
);
INSERT INTO scheduler_leader VALUES(1,'',0,'1970-01-01 00:00:00+00:00',1,0,'');
CREATE TABLE scheduler_policies (
 id TEXT PRIMARY KEY, scope TEXT NOT NULL CHECK(scope IN ('global','adapter')), scope_id TEXT NOT NULL,
 max_concurrent INTEGER NOT NULL CHECK(max_concurrent BETWEEN 1 AND 10),
 policy_json TEXT NOT NULL CHECK(json_valid(policy_json)), revision INTEGER NOT NULL CHECK(revision>0), UNIQUE(scope,scope_id)
);
INSERT INTO scheduler_policies VALUES('global','global','',3,'{"ID":"global","Scope":"global","ScopeID":"","MaxConcurrent":3,"CPURecoverPercent":65,"CPUSoftPercent":75,"CPUHardPercent":85,"RAMSoftPercent":25,"RAMHardPercent":15,"RAMFloorBytes":2147483648,"Revision":1}',1);
CREATE TABLE scheduler_admissions (
 id TEXT PRIMARY KEY, attempt_id TEXT UNIQUE REFERENCES workflow_attempts(id), workflow_id TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL DEFAULT '',
 session_id TEXT UNIQUE REFERENCES sessions(id), project_id TEXT NOT NULL DEFAULT '', harness TEXT NOT NULL CHECK(length(harness)>0), mode TEXT NOT NULL DEFAULT '' CHECK(mode IN ('','chat','tui')),
 client_request_id TEXT UNIQUE, request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64),
 owner_id TEXT NOT NULL, epoch INTEGER NOT NULL CHECK(epoch>0),
 state TEXT NOT NULL CHECK(state IN ('reserved','active','uncertain','released')), revision INTEGER NOT NULL CHECK(revision>0),
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
 CHECK(attempt_id IS NOT NULL OR client_request_id IS NOT NULL)
);
CREATE INDEX scheduler_admissions_occupied ON scheduler_admissions(state,harness);
CREATE TABLE budget_policies (
 id TEXT PRIMARY KEY, scope TEXT NOT NULL CHECK(scope IN ('global','workflow','task')), scope_id TEXT NOT NULL,
 currency TEXT NOT NULL CHECK(currency='USD'), mode TEXT NOT NULL CHECK(mode IN ('observe','soft','hard_admission')),
 period TEXT NOT NULL CHECK(period='lifetime' OR (scope='global' AND period='monthly_utc')),
 limit_nanos INTEGER NOT NULL CHECK(limit_nanos>=0), reservation_nanos INTEGER CHECK(reservation_nanos>=0),
 reservation_basis TEXT NOT NULL DEFAULT '', reservation_ref TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL CHECK(revision>0),
 UNIQUE(scope,scope_id,currency,period), CHECK((scope='global' AND scope_id='') OR (scope<>'global' AND length(scope_id)>0))
);
CREATE TABLE budget_reservations (
 admission_id TEXT NOT NULL REFERENCES scheduler_admissions(id), attempt_id TEXT REFERENCES workflow_attempts(id),
 policy_id TEXT NOT NULL REFERENCES budget_policies(id), window TEXT NOT NULL,
 amount_nanos INTEGER CHECK(amount_nanos>=0), basis TEXT NOT NULL, reference TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('reserved','consumed','released','uncertain')), revision INTEGER NOT NULL CHECK(revision>0),
 PRIMARY KEY(admission_id,policy_id,window), UNIQUE(attempt_id,policy_id,window)
);
CREATE TABLE workflow_usage_attributions (
 event_id INTEGER PRIMARY KEY REFERENCES model_usage_events(id), session_id TEXT NOT NULL, attempt_id TEXT NOT NULL DEFAULT '',
 workflow_id TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL DEFAULT '', occurred_at TIMESTAMP,
 currency TEXT NOT NULL, cost_nanos INTEGER CHECK(cost_nanos>=0), quality TEXT NOT NULL CHECK(quality IN ('EXACT','ESTIMATED','UNKNOWN')),
 revision INTEGER NOT NULL CHECK(revision>0), price_version TEXT NOT NULL DEFAULT ''
);
CREATE TABLE workflow_usage_corrections (
 correction_key TEXT PRIMARY KEY, event_id INTEGER NOT NULL REFERENCES workflow_usage_attributions(event_id),
 previous_revision INTEGER NOT NULL, revision INTEGER NOT NULL, old_cost_nanos INTEGER, new_cost_nanos INTEGER,
 reason_code TEXT NOT NULL, created_at TIMESTAMP NOT NULL, UNIQUE(event_id,revision)
);
CREATE TABLE scheduler_workflow_state (
 workflow_id TEXT PRIMARY KEY REFERENCES workflows(id), deficit INTEGER NOT NULL CHECK(deficit>=0), candidate_offset INTEGER NOT NULL DEFAULT 0 CHECK(candidate_offset>=0),
 weight INTEGER NOT NULL CHECK(weight BETWEEN 1 AND 5), paused INTEGER NOT NULL CHECK(paused IN (0,1)), revision INTEGER NOT NULL CHECK(revision>0)
);
CREATE TABLE scheduler_retry_due (
 workflow_id TEXT NOT NULL, task_id TEXT NOT NULL, attempt_id TEXT NOT NULL REFERENCES workflow_attempts(id), harness TEXT NOT NULL DEFAULT '', next_eligible_at TIMESTAMP NOT NULL, reason_code TEXT NOT NULL,
 policy_revision INTEGER NOT NULL CHECK(policy_revision>0), PRIMARY KEY(workflow_id,task_id),
 FOREIGN KEY(workflow_id,task_id) REFERENCES workflow_tasks(workflow_id,id)
);
CREATE TABLE scheduler_decisions (
 id INTEGER PRIMARY KEY AUTOINCREMENT, admission_id TEXT NOT NULL DEFAULT '', workflow_id TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL DEFAULT '',
 owner_id TEXT NOT NULL, epoch INTEGER NOT NULL, reason_code TEXT NOT NULL,
 policy_revisions_json TEXT NOT NULL CHECK(json_valid(policy_revisions_json)), created_at TIMESTAMP NOT NULL
);
CREATE TABLE scheduler_policy_changes (
 idempotency_key TEXT PRIMARY KEY, request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64),
 kind TEXT NOT NULL CHECK(kind IN ('resource','budget')), policy_id TEXT NOT NULL, result_json TEXT NOT NULL CHECK(json_valid(result_json)),
 actor_ref TEXT NOT NULL, reason TEXT NOT NULL, created_at TIMESTAMP NOT NULL
);
CREATE TRIGGER scheduler_admission_created AFTER INSERT ON scheduler_admissions BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','admission','id',NEW.id,'state',NEW.state,'revision',NEW.revision,'epoch',NEW.epoch));
END;
CREATE TRIGGER scheduler_admission_updated AFTER UPDATE ON scheduler_admissions BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','admission','id',NEW.id,'state',NEW.state,'revision',NEW.revision,'epoch',NEW.epoch));
END;
CREATE TRIGGER scheduler_policy_created AFTER INSERT ON scheduler_policies BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','policy','id',NEW.id,'revision',NEW.revision));
END;
CREATE TRIGGER scheduler_policy_updated AFTER UPDATE ON scheduler_policies BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','policy','id',NEW.id,'revision',NEW.revision));
END;
CREATE TRIGGER budget_policy_created AFTER INSERT ON budget_policies BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','budget','id',NEW.id,'revision',NEW.revision));
END;
CREATE TRIGGER budget_policy_updated AFTER UPDATE ON budget_policies BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','budget','id',NEW.id,'revision',NEW.revision));
END;
COMMIT;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
BEGIN IMMEDIATE;
DROP TABLE scheduler_decisions;
DROP TABLE scheduler_policy_changes;
DROP TABLE scheduler_retry_due;
DROP TABLE scheduler_workflow_state;
DROP TABLE workflow_usage_corrections;
DROP TABLE workflow_usage_attributions;
DROP TABLE budget_reservations;
DROP TABLE budget_policies;
DROP TABLE scheduler_admissions;
DROP TABLE scheduler_policies;
DROP TABLE scheduler_leader;
DELETE FROM change_log WHERE event_type='scheduler_updated';
COMMIT;
PRAGMA writable_schema = ON;
UPDATE sqlite_master SET sql=replace(sql, ' ''scheduler_updated'',','') WHERE type='table' AND name='change_log';
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
