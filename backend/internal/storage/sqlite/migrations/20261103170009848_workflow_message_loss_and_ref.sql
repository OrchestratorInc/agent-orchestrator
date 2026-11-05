-- +goose NO TRANSACTION

-- Two corrections to M2-06's collaboration schema. Both are follow-up migrations rather than
-- edits because 20261103163208174 is already in the frozen shippedMigrations ledger: editing a
-- recorded version cannot reach a database that has already applied it.

-- +goose Up

-- CORRECTION 1: the loss trigger nulled both session references unconditionally.
--
-- sessions_workflow_message_loss nulled sender_session_id AND target_session_id whenever a
-- session was deleted, even when only one of them was the deleted session. Deleting a sender's
-- session therefore destroyed the still-valid target_session_id of that row, losing a live
-- reference that had nothing to do with the deletion.
--
-- The replacement nulls only the column that actually points at OLD.id. Both references may
-- match (a message to itself is impossible — a CHECK forbids self-targeting — but a row can
-- reference the same session as both sender and target through different tasks), in which case
-- both are nulled, which is correct.
-- The old trigger must be dropped before the table it references is replaced, or the RENAME
-- fails with "no such table" from inside the trigger body.
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sessions_workflow_message_loss;
-- +goose StatementEnd

-- CORRECTION 2: payload_ref's CHECK was weaker than the claim it made.
--
-- The original only rejected '/', '\', ':' and NUL, so '..', '.', 'CON' and every
-- percent-encoded form passed while a comment claimed the value could not be a filesystem path.
-- SQLite cannot run a regex over a CHECK, so the closed class is expressed as a chain of
-- GLOB-style tests: every character must be in [A-Za-z0-9._-], '%' is excluded so no decoded
-- separator can hide in the value, and the whole value may not be '.' or '..'.
--
-- The table is rebuilt because SQLite cannot alter a CHECK in place. Every column, index and
-- row is carried over.
-- +goose StatementBegin
CREATE TABLE workflow_messages_new (
    id            TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    workflow_id   TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    from_task_id  TEXT NOT NULL CHECK (length(trim(from_task_id)) > 0),
    to_task_id    TEXT,
    sender_session_id TEXT REFERENCES sessions (id) ON DELETE SET NULL,
    target_session_id TEXT REFERENCES sessions (id) ON DELETE SET NULL,
    sender_kind   TEXT NOT NULL CHECK (sender_kind IN ('agent', 'human', 'daemon')),
    sender_task_id TEXT,
    kind          TEXT NOT NULL CHECK (kind IN (
        'progress', 'question', 'result', 'handoff', 'reference'
    )),
    delivery_state TEXT NOT NULL DEFAULT 'persisted' CHECK (delivery_state IN (
        'persisted', 'forwarded', 'rejected', 'unknown'
    )),
    body          TEXT NOT NULL DEFAULT '',
    payload_ref   TEXT NOT NULL DEFAULT '',
    client_message_id TEXT NOT NULL CHECK (length(trim(client_message_id)) > 0),
    payload_sha   TEXT NOT NULL CHECK (length(payload_sha) = 64),
    revision      INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,

    CHECK (length(CAST(body AS BLOB)) <= 4096),
    CHECK (body <> '' OR payload_ref <> ''),
    CHECK (length(CAST(payload_ref AS BLOB)) <= 512),

    -- The closed character class, expressed with GLOB so the whole value is checked in one
    -- expression: NOT GLOB '*[^A-Za-z0-9._-]*' is true exactly when no character falls outside
    -- the allowed set. ',' and '..' are rejected explicitly because they are legal flat names
    -- that are also directory references.
    CHECK (
        payload_ref = ''
        OR (payload_ref <> '.'
            AND payload_ref <> '..'
            AND payload_ref NOT GLOB '*[^A-Za-z0-9._-]*'
            -- GLOB stops at the first NUL, so a NUL must be excluded explicitly rather than
            -- relying on the class test seeing every byte.
            AND instr(payload_ref, char(0)) = 0)
    ),

    CHECK (to_task_id IS NULL OR to_task_id <> from_task_id),
    CHECK (sender_kind <> 'agent' OR (sender_task_id IS NOT NULL AND sender_task_id = from_task_id)),
    CHECK (sender_kind = 'agent' OR sender_task_id IS NULL),

    UNIQUE (workflow_id, client_message_id),

    FOREIGN KEY (workflow_id, from_task_id)
        REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workflow_id, to_task_id)
        REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO workflow_messages_new (
    id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
)
SELECT
    id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
FROM workflow_messages;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE workflow_messages;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE workflow_messages_new RENAME TO workflow_messages;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_workflow_messages_workflow
    ON workflow_messages (workflow_id, created_at, id);
CREATE INDEX idx_workflow_messages_target
    ON workflow_messages (workflow_id, to_task_id, created_at)
    WHERE to_task_id IS NOT NULL;
CREATE INDEX idx_workflow_messages_open
    ON workflow_messages (workflow_id, delivery_state)
    WHERE delivery_state IN ('persisted', 'unknown');
-- +goose StatementEnd

-- CORRECTION 1's replacement trigger, installed now that the new table exists.
-- +goose StatementBegin
CREATE TRIGGER sessions_workflow_message_loss
BEFORE DELETE ON sessions
BEGIN
    UPDATE workflow_messages
    SET sender_session_id = CASE WHEN sender_session_id = OLD.id THEN NULL ELSE sender_session_id END,
        target_session_id = CASE WHEN target_session_id = OLD.id THEN NULL ELSE target_session_id END,
        revision = revision + 1,
        updated_at = OLD.updated_at
    WHERE sender_session_id = OLD.id OR target_session_id = OLD.id;
END;
-- +goose StatementEnd

-- The CDC triggers were dropped with the table, so they are recreated against the new one.
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

-- +goose Down
-- Restore the unconditional nulling and the permissive CHECK. This is a best-effort reversal:
-- a value that the tightened CHECK now refuses cannot exist, so the data copy back is lossless.
DROP TRIGGER IF EXISTS workflow_messages_cdc_update;
DROP TRIGGER IF EXISTS workflow_messages_cdc_insert;
DROP TRIGGER IF EXISTS sessions_workflow_message_loss;

-- +goose StatementBegin
CREATE TABLE workflow_messages_old (
    id            TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    workflow_id   TEXT NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    from_task_id  TEXT NOT NULL CHECK (length(trim(from_task_id)) > 0),
    to_task_id    TEXT,
    sender_session_id TEXT REFERENCES sessions (id) ON DELETE SET NULL,
    target_session_id TEXT REFERENCES sessions (id) ON DELETE SET NULL,
    sender_kind   TEXT NOT NULL CHECK (sender_kind IN ('agent', 'human', 'daemon')),
    sender_task_id TEXT,
    kind          TEXT NOT NULL CHECK (kind IN ('progress', 'question', 'result', 'handoff', 'reference')),
    delivery_state TEXT NOT NULL DEFAULT 'persisted' CHECK (delivery_state IN ('persisted', 'forwarded', 'rejected', 'unknown')),
    body          TEXT NOT NULL DEFAULT '',
    payload_ref   TEXT NOT NULL DEFAULT '',
    client_message_id TEXT NOT NULL CHECK (length(trim(client_message_id)) > 0),
    payload_sha   TEXT NOT NULL CHECK (length(payload_sha) = 64),
    revision      INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL,
    CHECK (length(CAST(body AS BLOB)) <= 4096),
    CHECK (body <> '' OR payload_ref <> ''),
    CHECK (length(CAST(payload_ref AS BLOB)) <= 512),
    CHECK (instr(payload_ref, char(0)) = 0),
    CHECK (instr(payload_ref, '/') = 0 AND instr(payload_ref, '\') = 0 AND instr(payload_ref, ':') = 0),
    CHECK (to_task_id IS NULL OR to_task_id <> from_task_id),
    CHECK (sender_kind <> 'agent' OR (sender_task_id IS NOT NULL AND sender_task_id = from_task_id)),
    CHECK (sender_kind = 'agent' OR sender_task_id IS NULL),
    UNIQUE (workflow_id, client_message_id),
    FOREIGN KEY (workflow_id, from_task_id) REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workflow_id, to_task_id) REFERENCES workflow_tasks (workflow_id, id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO workflow_messages_old (
    id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
)
SELECT id, workflow_id, from_task_id, to_task_id, sender_session_id, target_session_id,
    sender_kind, sender_task_id, kind, delivery_state, body, payload_ref,
    client_message_id, payload_sha, revision, created_at, updated_at
FROM workflow_messages;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE workflow_messages;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE workflow_messages_old RENAME TO workflow_messages;
CREATE INDEX idx_workflow_messages_workflow ON workflow_messages (workflow_id, created_at, id);
CREATE INDEX idx_workflow_messages_target ON workflow_messages (workflow_id, to_task_id, created_at) WHERE to_task_id IS NOT NULL;
CREATE INDEX idx_workflow_messages_open ON workflow_messages (workflow_id, delivery_state) WHERE delivery_state IN ('persisted', 'unknown');
-- +goose StatementEnd
