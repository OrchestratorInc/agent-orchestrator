-- Add Hermes Agent to the sessions harness constraint without changing usage bindings.
-- Versions 0197-0202 are reserved for sibling harness migrations in flight.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''hermes'', ''fake''))')
WHERE type = 'table' AND name = 'sessions' AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''muse''%' AND sql LIKE '%''kimchi''%' AND sql LIKE '%''prime-agent''%' AND sql LIKE '%''omp''%' AND sql LIKE '%''gemini''%' AND sql LIKE '%''unreal-agent''%' AND sql LIKE '%''mimo-code''%' AND sql LIKE '%''codewhale''%' AND sql LIKE '%''deepseek-harness''%' AND sql LIKE '%''openhands''%' AND sql LIKE '%''command-code''%' AND sql NOT LIKE '%''hermes''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE hermes_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO hermes_schema_guard (valid)
SELECT CASE WHEN sql IS NULL THEN 0 WHEN sql LIKE '%''muse''%' AND sql LIKE '%''kimchi''%' AND sql LIKE '%''prime-agent''%' AND sql LIKE '%''omp''%' AND sql LIKE '%''gemini''%' AND sql LIKE '%''unreal-agent''%' AND sql LIKE '%''mimo-code''%' AND sql LIKE '%''codewhale''%' AND sql LIKE '%''deepseek-harness''%' AND sql LIKE '%''openhands''%' AND sql LIKE '%''command-code''%' AND instr(sql, '''hermes''') = 0 THEN 0 ELSE 1 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE hermes_schema_guard;
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback while native Hermes sessions would violate the restored CHECK.
-- +goose StatementBegin
CREATE TEMP TABLE hermes_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO hermes_schema_guard (valid) SELECT CASE WHEN EXISTS (SELECT 1 FROM sessions WHERE harness = 'hermes') THEN 0 ELSE 1 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE hermes_schema_guard;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''hermes'', ''fake''))', '''fake''))')
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
