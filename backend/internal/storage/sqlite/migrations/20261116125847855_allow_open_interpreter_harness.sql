-- Add Open Interpreter to the sessions harness constraint without changing usage bindings.
-- Replaces the unmerged draft numbers 0199, 0207, 0216 and 20261114174124485.
-- Draft databases that already allow 'open-interpreter' keep their constraint;
-- goose records this version once and WithAllowMissing tolerates the retired
-- draft receipts.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''open-interpreter'', ''fake''))')
WHERE type = 'table' AND name = 'sessions' AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%' AND sql NOT LIKE '%''open-interpreter''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS open_interpreter_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO open_interpreter_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (harness IN (') > 0
    AND instr(COALESCE(sql, ''), '''fake''))') > 0
    AND instr(COALESCE(sql, ''), '''open-interpreter''') > 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE open_interpreter_schema_guard;
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback if Open Interpreter rows would violate the restored CHECK
-- or the constraint no longer has the shape this migration knows how to
-- reverse. Later harnesses may follow Open Interpreter before 'fake'.
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS open_interpreter_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO open_interpreter_schema_guard (valid)
SELECT CASE WHEN EXISTS (SELECT 1 FROM sessions WHERE harness = 'open-interpreter')
    OR NOT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'sessions'
        AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%'
        AND instr(sql, '''open-interpreter''') > 0)
    THEN 0 ELSE 1 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE open_interpreter_schema_guard;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''open-interpreter'', ', '')
WHERE type = 'table' AND name = 'sessions'
    AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''fake''))%'
    AND instr(sql, '''open-interpreter''') > 0;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS open_interpreter_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO open_interpreter_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (harness IN (') > 0
    AND instr(COALESCE(sql, ''), '''fake''))') > 0
    AND instr(COALESCE(sql, ''), '''open-interpreter''') = 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE open_interpreter_schema_guard;
-- +goose StatementEnd
