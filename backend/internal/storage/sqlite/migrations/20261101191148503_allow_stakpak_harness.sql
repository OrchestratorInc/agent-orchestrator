-- Add Stakpak to the sessions harness constraint without changing usage bindings.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''stakpak'', ''fake''))')
WHERE type = 'table' AND name = 'sessions' AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%' AND sql NOT LIKE '%''stakpak''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS stakpak_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO stakpak_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (harness IN (') > 0
    AND instr(COALESCE(sql, ''), '''fake''))') > 0
    AND instr(COALESCE(sql, ''), '''stakpak''') > 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE stakpak_schema_guard;
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback if Stakpak rows would violate the restored CHECK or the
-- constraint no longer has the shape this migration knows how to reverse.
-- Stakpak need not precede fake immediately: later harnesses may follow it.
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS stakpak_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO stakpak_schema_guard (valid)
SELECT CASE WHEN EXISTS (SELECT 1 FROM sessions WHERE harness = 'stakpak')
    OR NOT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'sessions'
        AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%'
        AND instr(sql, '''stakpak''') > 0)
    THEN 0 ELSE 1 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE stakpak_schema_guard;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''stakpak'', ', '')
WHERE type = 'table' AND name = 'sessions'
    AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%'
    AND instr(sql, '''stakpak''') > 0;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS stakpak_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO stakpak_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (harness IN (') > 0
    AND instr(COALESCE(sql, ''), '''fake''))') > 0
    AND instr(COALESCE(sql, ''), '''stakpak''') = 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE stakpak_schema_guard;
-- +goose StatementEnd
