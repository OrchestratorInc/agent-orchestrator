-- Add 'skipped' to the automation_runs status constraint. A run is skipped when
-- the scheduler merges it into a later occurrence of the same automation; it
-- never ran, so recording it as 'failed' made history and filters misleading.
-- SQLite cannot ALTER a CHECK constraint, so rewrite the table's schema text.
-- writable_schema changes run outside a transaction; RESET forces SQLite to
-- reparse the schema.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''completed'', ''failed''))', '''completed'', ''failed'', ''skipped''))')
WHERE type = 'table' AND name = 'automation_runs'
    AND sql LIKE '%CHECK (status IN (%' AND sql LIKE '%''completed'', ''failed''))%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS automation_run_skipped_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO automation_run_skipped_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (status IN (') > 0
    AND instr(COALESCE(sql, ''), '''completed'', ''failed'', ''skipped''))') > 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'automation_runs') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE automation_run_skipped_schema_guard;
-- +goose StatementEnd

-- +goose Down
-- Skipped rows have no place in the restored constraint, so record them as the
-- failed rows earlier builds wrote for the same case. NO TRANSACTION means a
-- stop after the rewrite leaves this version applied with the old constraint;
-- the guard accepts that shape too, so a retried Down finishes.
-- +goose StatementBegin
UPDATE automation_runs
SET status = 'failed', error_message = COALESCE(error_message, 'Skipped: merged into a later run of this automation')
WHERE status = 'skipped';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''completed'', ''failed'', ''skipped''))', '''completed'', ''failed''))')
WHERE type = 'table' AND name = 'automation_runs'
    AND sql LIKE '%CHECK (status IN (%' AND sql LIKE '%''completed'', ''failed'', ''skipped''))%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS automation_run_skipped_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO automation_run_skipped_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (status IN (') > 0
    AND instr(COALESCE(sql, ''), '''completed'', ''failed''))') > 0
    AND instr(COALESCE(sql, ''), '''skipped''') = 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'automation_runs') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE automation_run_skipped_schema_guard;
-- +goose StatementEnd
