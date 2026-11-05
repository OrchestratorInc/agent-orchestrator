-- Durable audit trail for sensitive operator decisions.
--
-- APPEND-ONLY. There is deliberately no UPDATE and no DELETE statement: a correction is a
-- new row, so the process being audited cannot rewrite what was recorded about it.
--
-- These statements never carry a credential. token_fingerprint is the first 8 hex
-- characters of the credential's SHA-256 (principal.OperatorTokenFingerprint), which is
-- enough to answer "was the same credential used" and not enough to replay anything.

-- name: InsertWorkflowAudit :execrows
-- A replay is a no-op, not an error. The handler only calls this for a decision that was
-- actually applied, but a retried request that races a first one must not double-record, so
-- the primary key is the idempotency guarantee and a conflict inserts nothing.
INSERT INTO workflow_audit_log (
    id, actor_kind, actor_ref, workflow_id, attempt_id, request_id,
    decision, outcome, token_fingerprint, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: ListWorkflowAudits :many
-- Oldest first: a trail is read in the order it happened.
SELECT id, actor_kind, actor_ref, workflow_id, attempt_id, request_id,
    decision, outcome, token_fingerprint, created_at
FROM workflow_audit_log
WHERE workflow_id = ?
ORDER BY created_at, id
LIMIT ? OFFSET ?;
