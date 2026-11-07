-- Add Coder Agents to the sessions harness constraint without changing usage bindings.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''coder-agents'', ''fake''))')
WHERE type = 'table' AND name = 'sessions' AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%' AND sql NOT LIKE '%''coder-agents''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS coder_agents_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO coder_agents_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (harness IN (') > 0
    AND instr(COALESCE(sql, ''), '''fake''))') > 0
    AND instr(COALESCE(sql, ''), '''coder-agents''') > 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE coder_agents_schema_guard;
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback if Coder Agents rows would violate the restored CHECK or the
-- constraint no longer has the shape this migration knows how to reverse.
-- Coder Agents need not precede fake immediately: later harnesses may follow it.
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS coder_agents_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO coder_agents_schema_guard (valid)
SELECT CASE WHEN EXISTS (SELECT 1 FROM sessions WHERE harness = 'coder-agents')
    OR NOT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'sessions'
        AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%'
        AND instr(sql, '''coder-agents''') > 0)
    THEN 0 ELSE 1 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE coder_agents_schema_guard;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''coder-agents'', ', '')
WHERE type = 'table' AND name = 'sessions'
    AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%'
    AND instr(sql, '''coder-agents''') > 0;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS coder_agents_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO coder_agents_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (harness IN (') > 0
    AND instr(COALESCE(sql, ''), '''fake''))') > 0
    AND instr(COALESCE(sql, ''), '''coder-agents''') = 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE coder_agents_schema_guard;
-- +goose StatementEnd
