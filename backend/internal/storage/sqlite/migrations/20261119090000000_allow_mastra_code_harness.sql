-- Add Mastra Code to the sessions harness constraint without changing usage bindings.
-- SQLite cannot ALTER a CHECK constraint, so rewrite the sessions schema.
--
-- Every historical harness variant ends with the retained 'fake' fixture
-- harness, so anchoring on that tail widens whichever shape a database arrives
-- with instead of enumerating each one. writable_schema changes run outside a
-- transaction; RESET forces SQLite to reparse the schema.
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
UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''mastra-code'', ''fake''))')
WHERE type = 'table' AND name = 'sessions' AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%' AND sql NOT LIKE '%''mastra-code''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS mastra_code_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO mastra_code_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (harness IN (') > 0
    AND instr(COALESCE(sql, ''), '''fake''))') > 0
    AND instr(COALESCE(sql, ''), '''mastra-code''') > 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE mastra_code_schema_guard;
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback if Mastra Code rows would violate the restored CHECK or the
-- constraint no longer has the shape this migration knows how to reverse.
-- Mastra Code need not precede fake immediately: later harnesses may follow it.
-- NO TRANSACTION means a stop after the rewrite leaves this version applied
-- with Mastra Code already removed, so that restored shape is accepted too and
-- a retried Down finishes instead of needing a manual repair.
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS mastra_code_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO mastra_code_schema_guard (valid)
SELECT CASE WHEN EXISTS (SELECT 1 FROM sessions WHERE harness = 'mastra-code')
    OR NOT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'sessions'
        AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%'
        AND (instr(sql, '''mastra-code'', ') > 0 OR instr(sql, '''mastra-code''') = 0))
    THEN 0 ELSE 1 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE mastra_code_schema_guard;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''mastra-code'', ', '')
WHERE type = 'table' AND name = 'sessions'
    AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%'
    AND instr(sql, '''mastra-code'', ') > 0;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS mastra_code_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO mastra_code_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (harness IN (') > 0
    AND instr(COALESCE(sql, ''), '''fake''))') > 0
    AND instr(COALESCE(sql, ''), '''mastra-code''') = 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE mastra_code_schema_guard;
-- +goose StatementEnd
