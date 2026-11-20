-- Add 'none' to the pr.ci_state constraint: the commit has no checks at all.
-- GitHub returns a null statusCheckRollup for such a commit, which AO used to
-- store as 'unknown' (not yet observed), so the PR panel waited on checks that
-- would never arrive (#7044).
-- SQLite cannot ALTER a CHECK; use the established sqlite_master rewrite.
-- RESET reparses the schema after changes outside a transaction.
-- Downgrade requires clearing 'none' rows first.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (ci_state IN (''unknown'', ''pending'', ''passing'', ''failing''))',
    'CHECK (ci_state IN (''unknown'', ''none'', ''pending'', ''passing'', ''failing''))'
)
WHERE type = 'table' AND name = 'pr'
    AND instr(sql, 'CHECK (ci_state IN (''unknown'', ''pending'', ''passing'', ''failing''))') > 0;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS pr_ci_none_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO pr_ci_none_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (ci_state IN (''unknown'', ''none'', ''pending'', ''passing'', ''failing''))') > 0
    THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'pr') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE pr_ci_none_schema_guard;
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback if 'none' rows would violate the restored CHECK or the
-- constraint no longer has the shape this migration knows how to reverse.
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS pr_ci_none_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO pr_ci_none_schema_guard (valid)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pr WHERE ci_state = 'none')
    OR NOT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'pr'
        AND instr(sql, 'CHECK (ci_state IN (''unknown'', ''none'', ''pending'', ''passing'', ''failing''))') > 0)
    THEN 0 ELSE 1 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE pr_ci_none_schema_guard;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    sql,
    'CHECK (ci_state IN (''unknown'', ''none'', ''pending'', ''passing'', ''failing''))',
    'CHECK (ci_state IN (''unknown'', ''pending'', ''passing'', ''failing''))'
)
WHERE type = 'table' AND name = 'pr'
    AND instr(sql, 'CHECK (ci_state IN (''unknown'', ''none'', ''pending'', ''passing'', ''failing''))') > 0;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS pr_ci_none_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO pr_ci_none_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (ci_state IN (''unknown'', ''pending'', ''passing'', ''failing''))') > 0
    THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'pr') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE pr_ci_none_schema_guard;
-- +goose StatementEnd
