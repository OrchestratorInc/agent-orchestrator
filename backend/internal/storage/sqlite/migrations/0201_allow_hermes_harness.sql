-- Widen the sessions.harness CHECK to allow the Hermes adapter.
-- SQLite cannot ALTER a CHECK constraint, so rewrite the sessions schema.
--
-- The rewrite targets only fully-migrated databases: ones whose constraint
-- already carries 'command-code' (migration 0194, the last harness before this
-- one). A database that skipped an earlier harness migration matches nothing
-- here and stays untouched — the startup reconcile path widens it harness by
-- harness in order, so inserting Hermes there before the missing earlier
-- harnesses would break the exact-string repairs those need. writable_schema
-- changes run outside a transaction; RESET forces SQLite to reparse.
--
-- usage_bindings carries its own narrower harness CHECK (harnesses whose usage AO
-- can bind to a provider account). Prior harness migrations left it alone and
-- this one does too.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, '''command-code'', ''fake''))', '''command-code'', ''hermes'', ''fake''))')
WHERE type = 'table' AND name = 'sessions'
  AND sql LIKE '%CHECK (harness IN (%'
  AND sql LIKE '%''command-code''%'
  AND sql NOT LIKE '%''hermes''%';
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
SET sql = replace(sql, '''command-code'', ''hermes'', ''fake''))', '''command-code'', ''fake''))')
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
