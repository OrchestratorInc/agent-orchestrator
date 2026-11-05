-- +goose NO TRANSACTION
-- +goose Up
-- 只有未释放占位对 Session 唯一，历史执行和预算继续保留。
-- +goose StatementBegin
PRAGMA foreign_keys=OFF;
PRAGMA legacy_alter_table=ON;
BEGIN IMMEDIATE;
CREATE TABLE scheduler_admissions_rebuilt (
 id TEXT PRIMARY KEY, attempt_id TEXT UNIQUE REFERENCES workflow_attempts(id), workflow_id TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL DEFAULT '',
 session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL, project_id TEXT NOT NULL DEFAULT '', harness TEXT NOT NULL CHECK(length(harness)>0), mode TEXT NOT NULL DEFAULT '' CHECK(mode IN ('','chat','tui')),
 client_request_id TEXT UNIQUE, request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64),
 owner_id TEXT NOT NULL, epoch INTEGER NOT NULL CHECK(epoch>0),
 state TEXT NOT NULL CHECK(state IN ('reserved','active','uncertain','released')), revision INTEGER NOT NULL CHECK(revision>0),
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
 controller_generation TEXT NOT NULL DEFAULT '',
 CHECK(attempt_id IS NOT NULL OR client_request_id IS NOT NULL)
);
INSERT INTO scheduler_admissions_rebuilt (id,attempt_id,workflow_id,task_id,session_id,project_id,harness,mode,client_request_id,request_fingerprint,owner_id,epoch,state,revision,created_at,updated_at) SELECT id,attempt_id,workflow_id,task_id,session_id,project_id,harness,mode,client_request_id,request_fingerprint,owner_id,epoch,state,revision,created_at,updated_at FROM scheduler_admissions;
DROP TABLE scheduler_admissions;
ALTER TABLE scheduler_admissions_rebuilt RENAME TO scheduler_admissions;
CREATE UNIQUE INDEX scheduler_admissions_one_execution ON scheduler_admissions(session_id) WHERE state<>'released';
-- 首次异步启动在 Session 发布代际时固定归属；后续恢复必须先通过准入 CAS。
CREATE TRIGGER scheduler_initial_session_generation AFTER UPDATE OF runtime_launch_id,controller_generation ON sessions
WHEN NEW.is_terminated=0 AND (CASE WHEN NEW.session_mode='chat' THEN NEW.controller_generation ELSE NEW.runtime_launch_id END)<>''
BEGIN
 UPDATE scheduler_admissions SET controller_generation=CASE WHEN NEW.session_mode='chat' THEN NEW.controller_generation ELSE NEW.runtime_launch_id END,
 revision=revision+1,updated_at=NEW.updated_at
 WHERE session_id=NEW.id AND state='active' AND controller_generation='';
END;
CREATE INDEX scheduler_admissions_occupied ON scheduler_admissions(state,harness);
CREATE TRIGGER scheduler_admission_created AFTER INSERT ON scheduler_admissions BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','admission','id',NEW.id,'state',NEW.state,'revision',NEW.revision,'epoch',NEW.epoch));
END;
CREATE TRIGGER scheduler_admission_updated AFTER UPDATE ON scheduler_admissions BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','admission','id',NEW.id,'state',NEW.state,'revision',NEW.revision,'epoch',NEW.epoch));
END;
COMMIT;
PRAGMA legacy_alter_table=OFF;
PRAGMA foreign_keys=ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 拒绝不可表示的多次历史执行必须发生在关闭外键和开始重建事务之前。
CREATE TEMP TABLE IF NOT EXISTS execution_generation_down_guard(ok INTEGER CHECK(ok=1));
DELETE FROM execution_generation_down_guard;
INSERT INTO execution_generation_down_guard SELECT NOT EXISTS(
 SELECT session_id FROM scheduler_admissions WHERE session_id IS NOT NULL GROUP BY session_id HAVING count(*)>1
);
DROP TABLE execution_generation_down_guard;
PRAGMA foreign_keys=OFF;
PRAGMA legacy_alter_table=ON;
BEGIN IMMEDIATE;
DROP TRIGGER scheduler_initial_session_generation;
CREATE TABLE scheduler_admissions_rebuilt (
 id TEXT PRIMARY KEY, attempt_id TEXT UNIQUE REFERENCES workflow_attempts(id), workflow_id TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL DEFAULT '',
 session_id TEXT UNIQUE REFERENCES sessions(id), project_id TEXT NOT NULL DEFAULT '', harness TEXT NOT NULL CHECK(length(harness)>0), mode TEXT NOT NULL DEFAULT '' CHECK(mode IN ('','chat','tui')),
 client_request_id TEXT UNIQUE, request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64),
 owner_id TEXT NOT NULL, epoch INTEGER NOT NULL CHECK(epoch>0),
 state TEXT NOT NULL CHECK(state IN ('reserved','active','uncertain','released')), revision INTEGER NOT NULL CHECK(revision>0),
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
 CHECK(attempt_id IS NOT NULL OR client_request_id IS NOT NULL)
);
-- 旧 schema 无法表示多次执行；唯一约束拒绝回退，绝不丢弃历史。
INSERT INTO scheduler_admissions_rebuilt (id,attempt_id,workflow_id,task_id,session_id,project_id,harness,mode,client_request_id,request_fingerprint,owner_id,epoch,state,revision,created_at,updated_at) SELECT id,attempt_id,workflow_id,task_id,session_id,project_id,harness,mode,client_request_id,request_fingerprint,owner_id,epoch,state,revision,created_at,updated_at FROM scheduler_admissions;
DROP TABLE scheduler_admissions;
ALTER TABLE scheduler_admissions_rebuilt RENAME TO scheduler_admissions;
CREATE INDEX scheduler_admissions_occupied ON scheduler_admissions(state,harness);
CREATE TRIGGER scheduler_admission_created AFTER INSERT ON scheduler_admissions BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','admission','id',NEW.id,'state',NEW.state,'revision',NEW.revision,'epoch',NEW.epoch));
END;
CREATE TRIGGER scheduler_admission_updated AFTER UPDATE ON scheduler_admissions BEGIN
 INSERT INTO change_log(event_type,payload) VALUES('scheduler_updated',json_object('kind','admission','id',NEW.id,'state',NEW.state,'revision',NEW.revision,'epoch',NEW.epoch));
END;
COMMIT;
PRAGMA legacy_alter_table=OFF;
PRAGMA foreign_keys=ON;
-- +goose StatementEnd
