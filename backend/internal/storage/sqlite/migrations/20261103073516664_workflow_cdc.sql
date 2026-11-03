-- +goose NO TRANSACTION

-- Capture workflow lifecycle changes in change_log so the desktop can react
-- immediately when a workflow, task, or attempt changes state.
--
-- Scope: workflow events carry project_id = NULL and session_id = NULL, and the
-- identifiers live in the payload instead. A workflow deliberately spans
-- several registered projects, so attributing an event to exactly one of them
-- would be false. A project fan-out cannot fix that either: at workflows INSERT
-- time the workflow_repositories rows do not exist yet, so an INSERT..SELECT
-- keyed on them would silently emit ZERO events for workflow_created. NULL is
-- the truthful scope, change_log.project_id is nullable (0140), and every
-- current consumer (SSE /api/v1/events, terminal sessionUpdate fan-out) forwards
-- project_id verbatim without depending on it.
--
-- The event_type CHECK is widened in place through sqlite_master: rebuilding
-- change_log would mean hand-recreating its dozens of CDC triggers, which is a
-- much larger blast radius than rewriting one CHECK list. writable_schema edits
-- cannot run inside a transaction, and replace() reports a changed row even when
-- the anchor does not match, so the widening is guarded by a postcondition on
-- the stored schema text itself.

-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    '            ''review_run_created'',',
    '            ''review_run_created'',
            ''workflow_created'',
            ''workflow_updated'',
            ''workflow_task_updated'',
            ''workflow_attempt_updated'','
)
WHERE type = 'table' AND name = 'change_log';
-- +goose StatementEnd

-- RESET disables writable_schema and forces this connection to discard its
-- cached schema before the guard below and the triggers are evaluated.
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TEMP TABLE workflow_cdc_check_guard (widened INTEGER CHECK (widened = 4));
INSERT INTO workflow_cdc_check_guard
SELECT
    (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_created''') > 0)
  + (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_updated''') > 0)
  + (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_task_updated''') > 0)
  + (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_attempt_updated''') > 0);
DROP TABLE workflow_cdc_check_guard;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflows_cdc_insert
AFTER INSERT ON workflows
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_created',
        json_object('kind', 'workflow', 'workflowId', NEW.id, 'state', NEW.state,
                    'revision', NEW.revision),
        NEW.updated_at);
END;

CREATE TRIGGER workflows_cdc_update
AFTER UPDATE ON workflows
WHEN OLD.revision <> NEW.revision OR OLD.state <> NEW.state
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_updated',
        json_object('kind', 'workflow', 'workflowId', NEW.id, 'state', NEW.state,
                    'revision', NEW.revision),
        NEW.updated_at);
END;

CREATE TRIGGER workflow_tasks_cdc_insert
AFTER INSERT ON workflow_tasks
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_task_updated',
        json_object('kind', 'workflow_task', 'workflowId', NEW.workflow_id,
                    'taskId', NEW.id, 'state', NEW.state, 'revision', NEW.revision),
        NEW.updated_at);
END;

CREATE TRIGGER workflow_tasks_cdc_update
AFTER UPDATE ON workflow_tasks
WHEN OLD.revision <> NEW.revision OR OLD.state <> NEW.state
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_task_updated',
        json_object('kind', 'workflow_task', 'workflowId', NEW.workflow_id,
                    'taskId', NEW.id, 'state', NEW.state, 'revision', NEW.revision),
        NEW.updated_at);
END;

-- The WHEN guard is state-only on purpose: a claimed attempt rewrites
-- heartbeat_at/lease_until/updated_at on every lease operation, so guarding on
-- "something changed" would flood change_log and the SSE stream with events no
-- consumer can act on.
CREATE TRIGGER workflow_attempts_cdc_insert
AFTER INSERT ON workflow_attempts
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_attempt_updated',
        json_object('kind', 'workflow_attempt', 'workflowId', NEW.workflow_id,
                    'taskId', NEW.task_id, 'attemptId', NEW.id,
                    'attemptNo', NEW.attempt_no, 'state', NEW.state),
        NEW.updated_at);
END;

CREATE TRIGGER workflow_attempts_cdc_update
AFTER UPDATE ON workflow_attempts
WHEN OLD.state <> NEW.state
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_attempt_updated',
        json_object('kind', 'workflow_attempt', 'workflowId', NEW.workflow_id,
                    'taskId', NEW.task_id, 'attemptId', NEW.id,
                    'attemptNo', NEW.attempt_no, 'state', NEW.state),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS workflow_attempts_cdc_update;
DROP TRIGGER IF EXISTS workflow_attempts_cdc_insert;
DROP TRIGGER IF EXISTS workflow_tasks_cdc_update;
DROP TRIGGER IF EXISTS workflow_tasks_cdc_insert;
DROP TRIGGER IF EXISTS workflows_cdc_update;
DROP TRIGGER IF EXISTS workflows_cdc_insert;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    '            ''review_run_created'',
            ''workflow_created'',
            ''workflow_updated'',
            ''workflow_task_updated'',
            ''workflow_attempt_updated'',',
    '            ''review_run_created'','
)
WHERE type = 'table' AND name = 'change_log';
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TEMP TABLE workflow_cdc_check_guard (narrowed INTEGER CHECK (narrowed = 0));
INSERT INTO workflow_cdc_check_guard
SELECT
    (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_created''') > 0)
  + (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_updated''') > 0)
  + (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_task_updated''') > 0)
  + (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_attempt_updated''') > 0);
DROP TABLE workflow_cdc_check_guard;
-- +goose StatementEnd
