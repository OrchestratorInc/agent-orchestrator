-- +goose NO TRANSACTION

-- Durable audit trail for sensitive operator decisions.
--
-- The M2 operator surfaces (resolve an attempt, decide a task request) already call
-- principal.AuditRecorder, and a wired recorder that fails fails the request. Until this
-- migration there was no table for it, so every deployment ran with a nil recorder and
-- recorded nothing.
--
-- Four properties shape the schema:
--
--  1. APPEND-ONLY. No UPDATE or DELETE statement exists in the queries, and nothing here
--     grants one. A correction is a new row, so history cannot be rewritten by the process
--     being audited.
--
--  2. NO SECRETS. The credential is stored as its 8-hex FINGERPRINT only
--     (principal.OperatorTokenFingerprint); no token, message body, or prompt column exists.
--     An audit trail that leaks what it audits is a second disclosure.
--
--  3. NOT a foreign-key child. workflow_id is a plain TEXT column with no REFERENCES: an
--     audit row must survive the deletion of the workflow it describes. A cascade would let
--     deleting a workflow erase the record of who approved it.
--
--  4. No change_log trigger. The audit trail is read by an operator, not streamed to the UI;
--     adding a trigger would also make every decision emit a CDC event that no consumer
--     handles, and AGENTS.md forbids parallel manual CDC instead of triggers.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE workflow_audit_log (
    id               TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    actor_kind       TEXT NOT NULL CHECK (actor_kind IN ('operator', 'agent')),
    actor_ref        TEXT NOT NULL CHECK (length(trim(actor_ref)) > 0),
    workflow_id      TEXT NOT NULL CHECK (length(trim(workflow_id)) > 0),
    attempt_id       TEXT NOT NULL DEFAULT '',
    request_id       TEXT NOT NULL DEFAULT '',
    decision         TEXT NOT NULL CHECK (length(trim(decision)) > 0),
    outcome          TEXT NOT NULL DEFAULT '',
    -- The first 8 hex characters of the credential's SHA-256. Never the credential.
    token_fingerprint TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL CHECK (length(trim(created_at)) > 0)
);
-- +goose StatementEnd

-- Reads are "this workflow's decisions, oldest first", which is the order an operator reads
-- a trail in.
-- +goose StatementBegin
CREATE INDEX idx_workflow_audit_log_workflow
    ON workflow_audit_log (workflow_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_workflow_audit_log_workflow;
DROP TABLE IF EXISTS workflow_audit_log;
-- +goose StatementEnd
