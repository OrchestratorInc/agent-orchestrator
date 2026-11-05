-- +goose NO TRANSACTION

-- M2 interventions: Steer, Queue, and Stop & Replan.
--
-- Every row is one request a caller made about one attempt's session, and the row is the
-- durable receipt. Three properties shape the schema:
--
--  1. session_id is NULLABLE IN EVERY STATE. This is the M2-03 lesson applied
--     deliberately: putting session_id inside a state/coherence CHECK is what made
--     session deletion fail with a raw constraint string and pinned sessions forever.
--     An intervention is history about a session that may be deleted, so the loss is
--     recorded by a BEFORE DELETE trigger (see below) instead of being forbidden.
--
--  2. No message body. The row stores client_message_id and chat/provider turn ids only.
--     Intervention text contains user prompts and sometimes credentials pasted into a
--     chat; change_log feeds SSE and must never carry it.
--
--  3. The mode↔state CHECK is the database half of domain.WorkflowInterventionMode
--     .AllowsState. A steer cannot be recorded as stop_confirmed (which would freeze a
--     task that was never stopped) and a stop cannot be recorded as applied (which would
--     claim a message landed).

-- +goose Up

-- The event_type CHECK is widened in place through sqlite_master, using the same idiom as
-- 20261103073516664_workflow_cdc.sql: rebuilding change_log would mean hand-recreating its
-- dozens of CDC triggers, and replace() reports a changed row even when the anchor does not
-- match, so the widening is guarded by a postcondition on the stored schema text itself.
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    '            ''workflow_attempt_updated'',',
    '            ''workflow_attempt_updated'',
            ''workflow_intervention_updated'','
)
WHERE type = 'table' AND name = 'change_log';
-- +goose StatementEnd

-- RESET disables writable_schema and forces this connection to discard its cached schema
-- before the guard and the trigger below are evaluated.
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TEMP TABLE workflow_intervention_cdc_check_guard (widened INTEGER CHECK (widened = 1));
INSERT INTO workflow_intervention_cdc_check_guard
SELECT
    (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log'
        AND instr(sql, '''workflow_intervention_updated''') > 0);
DROP TABLE workflow_intervention_cdc_check_guard;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE workflow_interventions (
    id            TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    workflow_id   TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    task_id       TEXT NOT NULL,
    attempt_id    TEXT,
    mode          TEXT NOT NULL CHECK (mode IN ('steer', 'queue', 'stop')),
    state         TEXT NOT NULL CHECK (state IN (
        'requested', 'queued', 'delivered', 'applied', 'rejected', 'cancelled',
        'unknown', 'stop_requested', 'stop_confirmed', 'stop_unknown'
    )),

    -- Nullable in every state, by design. ON DELETE SET NULL can land from any state.
    session_id    TEXT REFERENCES sessions (id) ON DELETE SET NULL,

    controller_generation TEXT NOT NULL CHECK (length(trim(controller_generation)) > 0),

    client_message_id TEXT NOT NULL CHECK (length(trim(client_message_id)) > 0),
    -- The 64-character request fingerprint. Same key + same sha is a replay; same key
    -- with a different sha is a conflict, which is what stops a retried Stop from issuing
    -- a second interrupt for edited content.
    operation_sha     TEXT NOT NULL CHECK (length(operation_sha) = 64),

    target_provider_turn_id TEXT NOT NULL DEFAULT '',
    chat_turn_id            TEXT NOT NULL DEFAULT '',
    provider_turn_id        TEXT NOT NULL DEFAULT '',

    reason_code      TEXT NOT NULL DEFAULT '',
    queue_available  INTEGER NOT NULL DEFAULT 0 CHECK (queue_available IN (0, 1)),

    revision      INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,
    settled_at    TIMESTAMP,

    -- Idempotency: one client message id per workflow names exactly one intervention.
    UNIQUE (workflow_id, client_message_id),

    -- Mode↔state compatibility, mirrored from domain.AllowedStatesFor. A steer/queue row
    -- can never occupy a stop state and a stop row can never occupy a message state.
    CHECK (
        (mode IN ('steer', 'queue') AND state IN (
            'requested', 'queued', 'delivered', 'applied', 'rejected', 'cancelled', 'unknown'
        ))
        OR (mode = 'stop' AND state IN (
            'requested', 'stop_requested', 'stop_confirmed', 'stop_unknown', 'rejected'
        ))
    ),

    -- Only a steer names a target turn. Queue and stop must leave it empty, so a stop can
    -- never be mistaken for guidance aimed at a turn.
    CHECK (mode = 'steer' OR target_provider_turn_id = ''),

    FOREIGN KEY (workflow_id, task_id)
        REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_workflow_interventions_attempt
    ON workflow_interventions (workflow_id, task_id, attempt_id, created_at);

CREATE INDEX idx_workflow_interventions_state
    ON workflow_interventions (state, updated_at);

CREATE INDEX idx_workflow_interventions_session
    ON workflow_interventions (session_id)
    WHERE session_id IS NOT NULL;

-- A steer names the turn it joins; the chat service needs that id and a client needs it
-- back for correlation. It is empty for queue and stop.
CREATE INDEX idx_workflow_interventions_target_turn
    ON workflow_interventions (target_provider_turn_id)
    WHERE target_provider_turn_id <> '';
-- +goose StatementEnd

-- Session deletion must not be blocked by an intervention row, and the row must not keep
-- naming a session that no longer exists. This mirrors the M2-03 loss trigger: rewrite the
-- row first (to a state that reports "we no longer know"), then let ON DELETE SET NULL land
-- on the now-nullable column.
--
-- The state chosen depends on where the intervention was: a settled receipt is history and
-- keeps its state; anything unsettled becomes the UNCERTAIN state OF ITS OWN MODE.
--
-- The mode-awareness is load-bearing, not cosmetic. The table's mode↔state CHECK forbids
-- 'unknown' for mode='stop' (a stop's states are stop_requested/stop_confirmed/stop_unknown),
-- so rewriting every unsettled stop row to 'unknown' made the delete itself fail with a raw
-- CHECK constraint error — the same class of defect that pinned sessions in M2-03. A stop-mode
-- row therefore lands on stop_unknown, which is exactly the state the design expects to persist
-- while a human reconciles:
--
--   mode='steer'/'queue', settled  (applied|rejected|cancelled) → keep the state
--   mode='steer'/'queue', unsettled                             → 'unknown'
--   mode='stop',         settled   (stop_confirmed)             → keep the state
--   mode='stop',         unsettled                              → 'stop_unknown'
-- +goose StatementBegin
CREATE TRIGGER sessions_workflow_intervention_loss
BEFORE DELETE ON sessions
BEGIN
    UPDATE workflow_interventions
    SET state = CASE
            WHEN state IN ('applied', 'rejected', 'cancelled', 'stop_confirmed') THEN state
            WHEN mode = 'stop' THEN 'stop_unknown'
            ELSE 'unknown'
        END,
        reason_code = 'session_deleted',
        session_id = NULL,
        revision = revision + 1,
        updated_at = OLD.updated_at
    WHERE session_id = OLD.id;
END;
-- +goose StatementEnd

-- CDC: only a STATE change is an event a consumer can act on. Text is never in the
-- payload, so this cannot leak a prompt into the SSE stream.
-- +goose StatementBegin
CREATE TRIGGER workflow_interventions_cdc_insert
AFTER INSERT ON workflow_interventions
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_intervention_updated',
        json_object('kind', 'workflow_intervention', 'workflowId', NEW.workflow_id,
                    'taskId', NEW.task_id, 'interventionId', NEW.id, 'mode', NEW.mode,
                    'state', NEW.state, 'reasonCode', NEW.reason_code),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_interventions_cdc_update
AFTER UPDATE ON workflow_interventions
WHEN OLD.state <> NEW.state
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_intervention_updated',
        json_object('kind', 'workflow_intervention', 'workflowId', NEW.workflow_id,
                    'taskId', NEW.task_id, 'interventionId', NEW.id, 'mode', NEW.mode,
                    'state', NEW.state, 'reasonCode', NEW.reason_code),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS workflow_interventions_cdc_update;
DROP TRIGGER IF EXISTS workflow_interventions_cdc_insert;
DROP TRIGGER IF EXISTS sessions_workflow_intervention_loss;
DROP INDEX IF EXISTS idx_workflow_interventions_target_turn;
DROP INDEX IF EXISTS idx_workflow_interventions_session;
DROP INDEX IF EXISTS idx_workflow_interventions_state;
DROP INDEX IF EXISTS idx_workflow_interventions_attempt;
DROP TABLE IF EXISTS workflow_interventions;

-- Retained intervention events must go before the CHECK is narrowed. They describe rows in a
-- table this rollback drops, so keeping them would (a) leave events about nothing and (b) fail
-- the narrowed event_type CHECK, making PRAGMA integrity_check report a corrupt database.
-- +goose StatementBegin
DELETE FROM change_log WHERE event_type = 'workflow_intervention_updated';
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    '            ''workflow_intervention_updated'',',
    ''
)
WHERE type = 'table' AND name = 'change_log';
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
