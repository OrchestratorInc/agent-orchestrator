-- +goose NO TRANSACTION

-- M2-06: tiered-autonomy collaboration messages and task requests.
--
-- Two tables, and the constraints below are the enforced form of design §6 rather than a
-- transcription of it. Where the design's proposal would have accepted the wrong fact, the
-- comment says which correction applies.
--
-- The two boundaries this schema exists to hold:
--
--  1. A message cannot cross into another workflow. workflow_tasks has UNIQUE
--     (workflow_id, id), so every task reference here is a COMPOSITE key on (workflow_id,
--     task_id). A bare FK to workflow_tasks(id) would happily accept a task belonging to a
--     different workflow.
--
--  2. A pending task request has no scheduling effect. Nothing but the decide path reads
--     request_json, no trigger writes workflow_tasks or workflow_edges, and this migration
--     installs no trigger that could materialize a task.

-- +goose Up

-- Correction 8: widen change_log.event_type in place, using the same idiom as
-- 20261103073516664_workflow_cdc.sql and 20261103160303854_workflow_interventions.sql.
-- replace() reports a changed row even when the anchor does not match, so the widening is
-- guarded by a postcondition on the stored schema text.
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    '            ''workflow_intervention_updated'',',
    '            ''workflow_intervention_updated'',
            ''workflow_message_created'',
            ''workflow_message_updated'',
            ''workflow_task_request_updated'','
)
WHERE type = 'table' AND name = 'change_log';
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- The guard requires all three new values present, so a partial (or missed) widening cannot
-- pass silently.
-- +goose StatementBegin
CREATE TEMP TABLE workflow_collab_cdc_check_guard (widened INTEGER CHECK (widened = 3));
INSERT INTO workflow_collab_cdc_check_guard
SELECT
    (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_message_created''') > 0)
  + (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_message_updated''') > 0)
  + (SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='change_log' AND instr(sql, '''workflow_task_request_updated''') > 0);
DROP TABLE IF EXISTS workflow_collab_cdc_check_guard;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE workflow_messages (
    id            TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),

    -- Correction 1 + 2: the workflow is carried on the row and has its own single-column FK
    -- so a NULL composite part cannot skip referential checking.
    workflow_id   TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,

    -- from_task_id is required: a collaboration message always originates from a task.
    from_task_id  TEXT NOT NULL CHECK (length(trim(from_task_id)) > 0),
    -- to_task_id is nullable: NULL means a broadcast to the whole workflow, which is a
    -- legitimate collaboration shape.
    to_task_id    TEXT,

    -- Correction 7: session ids are nullable everywhere and never appear in a state CHECK,
    -- so ON DELETE SET NULL can always land. The loss is recorded by the trigger below.
    sender_session_id TEXT REFERENCES sessions (id) ON DELETE SET NULL,
    target_session_id TEXT REFERENCES sessions (id) ON DELETE SET NULL,

    -- Sender authenticity is server-derived. `agent` exists here because
    -- domain.MessageOrigin deliberately does not have it: a workflow agent is not a chat
    -- origin, and any forward to Chat must map agent to automation (see the service).
    sender_kind   TEXT NOT NULL CHECK (sender_kind IN ('agent', 'human', 'daemon')),
    -- sender_task_id is non-null exactly when the sender is an agent.
    sender_task_id TEXT,

    kind          TEXT NOT NULL CHECK (kind IN (
        'progress', 'question', 'result', 'handoff', 'reference'
    )),
    -- delivery_state covers only the delivery concern. It never claims the receiver acted
    -- on the content.
    delivery_state TEXT NOT NULL DEFAULT 'persisted' CHECK (delivery_state IN (
        'persisted', 'forwarded', 'rejected', 'unknown'
    )),

    -- Correction 4: body is capped by BYTES (BLOB length), not characters, and a row may
    -- never be empty: either there is inline text or there is a payload reference.
    body          TEXT NOT NULL DEFAULT '',
    -- payload_ref is an opaque id into the artifact store. It is NOT a filesystem path: a
    -- path here would be a sandbox-escape vector, so the CHECK rejects separators and NUL to
    -- keep it from ever being usable as one.
    payload_ref   TEXT NOT NULL DEFAULT '',

    client_message_id TEXT NOT NULL CHECK (length(trim(client_message_id)) > 0),
    -- Correction 6: 64-char fingerprint. Same key + same sha replays; same key with a
    -- different sha conflicts.
    payload_sha   TEXT NOT NULL CHECK (length(payload_sha) = 64),

    revision      INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,

    CHECK (length(CAST(body AS BLOB)) <= 4096),
    CHECK (body <> '' OR payload_ref <> ''),
    CHECK (length(CAST(payload_ref AS BLOB)) <= 512),
    CHECK (instr(payload_ref, char(0)) = 0),
    CHECK (instr(payload_ref, '/') = 0 AND instr(payload_ref, '\') = 0 AND instr(payload_ref, ':') = 0),

    -- A broadcast has no target; a directed message does, and it may not target its sender.
    CHECK (to_task_id IS NULL OR to_task_id <> from_task_id),

    -- Sender coherence: an agent sender names its task, and that task is the origin.
    CHECK (sender_kind <> 'agent' OR (sender_task_id IS NOT NULL AND sender_task_id = from_task_id)),
    CHECK (sender_kind = 'agent' OR sender_task_id IS NULL),

    -- Correction 6: one client message id per workflow names exactly one message.
    UNIQUE (workflow_id, client_message_id),

    -- Corrections 1 + 2: composite references into workflow_tasks, each backed by the
    -- single-column workflow_id FK above.
    FOREIGN KEY (workflow_id, from_task_id)
        REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workflow_id, to_task_id)
        REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_workflow_messages_workflow
    ON workflow_messages (workflow_id, created_at, id);

CREATE INDEX idx_workflow_messages_target
    ON workflow_messages (workflow_id, to_task_id, created_at)
    WHERE to_task_id IS NOT NULL;

-- Queue-depth accounting reads this: open messages for a workflow, counted inside the insert
-- transaction.
CREATE INDEX idx_workflow_messages_open
    ON workflow_messages (workflow_id, delivery_state)
    WHERE delivery_state IN ('persisted', 'unknown');
-- +goose StatementEnd

-- Task/resource requests. A row is a REQUEST awaiting a decision; it is not a task.
-- +goose StatementBegin
CREATE TABLE workflow_task_requests (
    id            TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    workflow_id   TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,

    requester_task_id TEXT NOT NULL CHECK (length(trim(requester_task_id)) > 0),

    request_kind  TEXT NOT NULL CHECK (request_kind IN (
        'create_task', 'add_dependency', 'expand_repos', 'increase_concurrency', 'increase_budget'
    )),
    -- Correction 5: canonicalised request JSON. Nothing outside the decide path may read it.
    request_json  TEXT NOT NULL CHECK (json_valid(request_json)),
    -- Correction 6: fingerprint over the canonical JSON. Recomputing it on decide is the
    -- tamper check.
    request_sha   TEXT NOT NULL CHECK (length(request_sha) = 64),

    state         TEXT NOT NULL DEFAULT 'pending' CHECK (state IN (
        'pending', 'approved', 'rejected', 'expired'
    )),

    -- Correction 5: the decider is decomposed, not one opaque string, so the
    -- pending/decided coherence is checkable and an agent decision is distinguishable.
    decided_by_kind TEXT CHECK (decided_by_kind IN ('human', 'daemon')),
    decided_by_ref  TEXT NOT NULL DEFAULT '',
    decided_at      TIMESTAMP,
    reason          TEXT NOT NULL DEFAULT '' CHECK (length(CAST(reason AS BLOB)) <= 1024),

    -- The workflow revision the request was raised against. Decide compares it, so a request
    -- cannot be approved after the graph moved underneath it.
    workflow_revision INTEGER NOT NULL CHECK (workflow_revision > 0),

    revision      INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,

    CHECK (length(CAST(request_json AS BLOB)) <= 8192),

    -- Correction 5: decided fields are populated exactly for a decided state.
    CHECK (
        (state = 'pending' AND decided_by_kind IS NULL AND decided_at IS NULL)
        OR (state IN ('approved', 'rejected') AND decided_by_kind IS NOT NULL AND decided_at IS NOT NULL)
        OR (state = 'expired' AND decided_at IS NOT NULL)
    ),

    UNIQUE (workflow_id, id),
    FOREIGN KEY (workflow_id, requester_task_id)
        REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_workflow_task_requests_workflow
    ON workflow_task_requests (workflow_id, state, created_at);

CREATE INDEX idx_workflow_task_requests_pending
    ON workflow_task_requests (workflow_id, requester_task_id)
    WHERE state = 'pending';
-- +goose StatementEnd

-- Correction 7: session deletion must not be blocked by a message row, and the row must not
-- keep naming a session that no longer exists. Same BEFORE DELETE pattern as M2-03/M2-05:
-- rewrite first, then let ON DELETE SET NULL land on the now-nullable column.
-- +goose StatementBegin
CREATE TRIGGER sessions_workflow_message_loss
BEFORE DELETE ON sessions
BEGIN
    UPDATE workflow_messages
    SET sender_session_id = NULL,
        target_session_id = NULL,
        revision = revision + 1,
        updated_at = OLD.updated_at
    WHERE sender_session_id = OLD.id OR target_session_id = OLD.id;
END;
-- +goose StatementEnd

-- CDC: ids/kind/state only. The payload must never carry body, payload_ref contents,
-- request_json, or reason — those contain user text and sometimes pasted credentials.
-- +goose StatementBegin
CREATE TRIGGER workflow_messages_cdc_insert
AFTER INSERT ON workflow_messages
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_message_created',
        json_object('kind', 'workflow_message', 'workflowId', NEW.workflow_id,
                    'messageId', NEW.id, 'fromTaskId', NEW.from_task_id,
                    'toTaskId', NEW.to_task_id, 'messageKind', NEW.kind,
                    'senderKind', NEW.sender_kind, 'deliveryState', NEW.delivery_state),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- A delivery-state change is the only update a consumer can act on.
-- +goose StatementBegin
CREATE TRIGGER workflow_messages_cdc_update
AFTER UPDATE ON workflow_messages
WHEN OLD.delivery_state <> NEW.delivery_state
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_message_updated',
        json_object('kind', 'workflow_message', 'workflowId', NEW.workflow_id,
                    'messageId', NEW.id, 'toTaskId', NEW.to_task_id,
                    'deliveryState', NEW.delivery_state),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_task_requests_cdc_insert
AFTER INSERT ON workflow_task_requests
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_task_request_updated',
        json_object('kind', 'workflow_task_request', 'workflowId', NEW.workflow_id,
                    'requestId', NEW.id, 'requesterTaskId', NEW.requester_task_id,
                    'requestKind', NEW.request_kind, 'state', NEW.state),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_task_requests_cdc_update
AFTER UPDATE ON workflow_task_requests
WHEN OLD.state <> NEW.state
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'workflow_task_request_updated',
        json_object('kind', 'workflow_task_request', 'workflowId', NEW.workflow_id,
                    'requestId', NEW.id, 'requestKind', NEW.request_kind, 'state', NEW.state),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- Deliberately absent: any trigger that writes workflow_tasks, workflow_edges, or
-- workflow_task_repos in response to a request. A pending request has no scheduling effect,
-- and an approved one still cannot materialize anything because service/workflow has no
-- AddTask/AddEdge entry point. Enforcing that at the schema level as well as in the service
-- means a future "just approve it" shortcut cannot take effect silently.

-- +goose Down
DROP TRIGGER IF EXISTS workflow_task_requests_cdc_update;
DROP TRIGGER IF EXISTS workflow_task_requests_cdc_insert;
DROP TRIGGER IF EXISTS workflow_messages_cdc_update;
DROP TRIGGER IF EXISTS workflow_messages_cdc_insert;
DROP TRIGGER IF EXISTS sessions_workflow_message_loss;
DROP INDEX IF EXISTS idx_workflow_task_requests_pending;
DROP INDEX IF EXISTS idx_workflow_task_requests_workflow;
DROP TABLE IF EXISTS workflow_task_requests;
DROP INDEX IF EXISTS idx_workflow_messages_open;
DROP INDEX IF EXISTS idx_workflow_messages_target;
DROP INDEX IF EXISTS idx_workflow_messages_workflow;
DROP TABLE IF EXISTS workflow_messages;

-- Retained message/request events must go before the CHECK is narrowed, for the same reason
-- the intervention rollback clears its own: they describe tables this rollback drops, and
-- leaving them behind fails the narrowed CHECK and corrupts integrity_check.
-- +goose StatementBegin
DELETE FROM change_log
WHERE event_type IN (
    'workflow_message_created',
    'workflow_message_updated',
    'workflow_task_request_updated'
);
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    '            ''workflow_message_created'',
            ''workflow_message_updated'',
            ''workflow_task_request_updated'',',
    ''
)
WHERE type = 'table' AND name = 'change_log';
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
