-- Antigravity (agy) accounts: an account is an agy folder (--gemini-dir) that
-- keeps its own sign-in in a file. Add the provider to every account table's
-- provider constraint. SQLite cannot ALTER a CHECK constraint, so rewrite the
-- tables' schema text; their rows, keys and indexes are unchanged.
-- writable_schema changes run outside a transaction; RESET forces SQLite to
-- reparse the schema.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, 'CHECK (provider IN (''claude'', ''codex''))', 'CHECK (provider IN (''claude'', ''codex'', ''antigravity''))')
WHERE type = 'table'
    AND name IN ('agent_accounts', 'agent_account_defaults', 'session_agent_accounts', 'agent_limit_readings')
    AND instr(sql, 'CHECK (provider IN (''claude'', ''codex''))') > 0;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS antigravity_account_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO antigravity_account_schema_guard (valid)
SELECT CASE WHEN (
    SELECT COUNT(*) FROM sqlite_master
    WHERE type = 'table'
        AND name IN ('agent_accounts', 'agent_account_defaults', 'session_agent_accounts', 'agent_limit_readings')
        AND instr(sql, 'CHECK (provider IN (''claude'', ''codex'', ''antigravity''))') > 0
) = 4 THEN 1 ELSE 0 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE antigravity_account_schema_guard;
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback while any Antigravity row would violate the restored CHECK.
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS antigravity_account_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO antigravity_account_schema_guard (valid)
SELECT CASE WHEN EXISTS (SELECT 1 FROM agent_accounts WHERE provider = 'antigravity')
    OR EXISTS (SELECT 1 FROM agent_account_defaults WHERE provider = 'antigravity')
    OR EXISTS (SELECT 1 FROM session_agent_accounts WHERE provider = 'antigravity')
    OR EXISTS (SELECT 1 FROM agent_limit_readings WHERE provider = 'antigravity')
    THEN 0 ELSE 1 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE antigravity_account_schema_guard;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, 'CHECK (provider IN (''claude'', ''codex'', ''antigravity''))', 'CHECK (provider IN (''claude'', ''codex''))')
WHERE type = 'table'
    AND name IN ('agent_accounts', 'agent_account_defaults', 'session_agent_accounts', 'agent_limit_readings');
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
