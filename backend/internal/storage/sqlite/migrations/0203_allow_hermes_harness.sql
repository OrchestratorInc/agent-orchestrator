-- Add Hermes Agent to the sessions harness constraint without changing usage bindings.
-- Versions 0197-0202 are reserved for sibling harness migrations in flight.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''hermes'', ''fake''))')
WHERE type = 'table' AND name = 'sessions' AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%' AND sql NOT LIKE '%''hermes''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE hermes_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO hermes_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), '''hermes''') > 0 THEN 1 ELSE 0 END
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
