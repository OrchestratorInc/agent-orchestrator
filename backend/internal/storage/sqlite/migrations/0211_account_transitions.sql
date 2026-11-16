-- A move to another provider account is the same durable controller handoff as
-- an interface switch: the old controller stops, the new one resumes the same
-- native conversation, and a failure restores the old one. The account fields
-- name both ends; the mode then stays the same.
--
-- SQLite cannot ALTER a CHECK constraint, so the mode-change invariant is
-- widened with the same surgical sqlite_master rewrite the harness migrations
-- use: a transition must change the mode or the account.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
ALTER TABLE session_interface_transitions ADD COLUMN source_account_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE session_interface_transitions ADD COLUMN target_account_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (source_mode <> target_mode)',
    'CHECK (source_mode <> target_mode OR target_account_id <> '''')'
)
WHERE type = 'table' AND name = 'session_interface_transitions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (source_mode <> target_mode OR target_account_id <> '''')',
    'CHECK (source_mode <> target_mode)'
)
WHERE type = 'table' AND name = 'session_interface_transitions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE session_interface_transitions DROP COLUMN target_account_id;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE session_interface_transitions DROP COLUMN source_account_id;
-- +goose StatementEnd
