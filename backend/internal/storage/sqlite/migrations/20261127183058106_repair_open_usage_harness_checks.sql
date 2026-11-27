-- Repair usage tables whose harness and source-kind CHECKs were never opened.
--
-- 20261103030401452_open_usage_source_kinds rebuilt usage_bindings with
-- CHECK (trim(harness) <> '') and usage_sources with CHECK (trim(kind) <> '').
-- A database that recorded that version from an earlier build of the change
-- kept the narrow lists ('claude-code', 'codex', 'kimi'), so goose never applies
-- the shipped rebuild. Every agy, OpenCode, Grok or other Chat session then
-- failed to bind usage and logged a CHECK failure on each hook and start.
--
-- Widening a CHECK cannot invalidate an existing row, so rewrite only the
-- constraint text in place, as the harness migrations do. The cut runs from
-- "CHECK (harness IN (" to the first "))", which closes the list whatever
-- harnesses it named. A database that already has the open shape is unchanged.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = substr(sql, 1, instr(sql, 'CHECK (harness IN (') - 1)
    || 'CHECK (trim(harness) <> '''')'
    || substr(sql, instr(sql, 'CHECK (harness IN (') + instr(substr(sql, instr(sql, 'CHECK (harness IN (')), '))') + 1)
WHERE type = 'table' AND name = 'usage_bindings' AND instr(sql, 'CHECK (harness IN (') > 0;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = substr(sql, 1, instr(sql, 'CHECK (kind IN (') - 1)
    || 'CHECK (trim(kind) <> '''')'
    || substr(sql, instr(sql, 'CHECK (kind IN (') + instr(substr(sql, instr(sql, 'CHECK (kind IN (')), '))') + 1)
WHERE type = 'table' AND name = 'usage_sources' AND instr(sql, 'CHECK (kind IN (') > 0;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS usage_check_repair_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO usage_check_repair_guard (valid)
SELECT CASE WHEN instr(bindings, 'CHECK (trim(harness) <> '''')') > 0
    AND instr(bindings, 'CHECK (harness IN (') = 0
    AND instr(sources, 'CHECK (trim(kind) <> '''')') > 0
    AND instr(sources, 'CHECK (kind IN (') = 0 THEN 1 ELSE 0 END
FROM (SELECT
    (SELECT COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name = 'usage_bindings') AS bindings,
    (SELECT COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name = 'usage_sources') AS sources);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE usage_check_repair_guard;
-- +goose StatementEnd

-- +goose Down
-- Nothing to undo: the open constraints are the shape the previous version
-- already defines, and narrowing them again would refuse existing rows.
