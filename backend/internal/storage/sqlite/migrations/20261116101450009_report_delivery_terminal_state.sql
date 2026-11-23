-- Allow terminal failed and abandoned delivery states for reports.
-- SQLite cannot ALTER a CHECK constraint in place, so rewrite the reports schema.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    replace(
        sql,
        'CHECK (delivery_state IN (''pending'', ''claimed'', ''acknowledged''))',
        'CHECK (delivery_state IN (''pending'', ''claimed'', ''acknowledged'', ''failed'', ''abandoned''))'
    ),
    'OR (delivery_state = ''acknowledged'' AND claim_token <> '''' AND claimed_at IS NOT NULL AND acknowledged_at IS NOT NULL)',
    'OR (delivery_state = ''acknowledged'' AND claim_token <> '''' AND claimed_at IS NOT NULL AND acknowledged_at IS NOT NULL) OR (delivery_state IN (''failed'', ''abandoned'') AND claim_token = '''' AND claimed_at IS NULL AND acknowledged_at IS NULL)'
)
WHERE type = 'table' AND name = 'reports';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS report_terminal_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO report_terminal_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (delivery_state IN (''pending'', ''claimed'', ''acknowledged'', ''failed'', ''abandoned''))') > 0
    AND instr(COALESCE(sql, ''), '''failed''') > 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'reports') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE report_terminal_schema_guard;
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback if failed or abandoned report rows would violate the restored CHECK
-- or the constraint no longer has the shape this migration knows how to reverse.
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS report_terminal_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO report_terminal_schema_guard (valid)
SELECT CASE WHEN EXISTS (SELECT 1 FROM reports WHERE delivery_state IN ('failed', 'abandoned'))
    OR NOT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'reports'
        AND instr(sql, '''failed''') > 0)
    THEN 0 ELSE 1 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE report_terminal_schema_guard;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(
    replace(
        sql,
        'CHECK (delivery_state IN (''pending'', ''claimed'', ''acknowledged'', ''failed'', ''abandoned''))',
        'CHECK (delivery_state IN (''pending'', ''claimed'', ''acknowledged''))'
    ),
    ' OR (delivery_state IN (''failed'', ''abandoned'') AND claim_token = '''' AND claimed_at IS NULL AND acknowledged_at IS NULL)',
    ''
)
WHERE type = 'table' AND name = 'reports';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS report_terminal_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO report_terminal_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'CHECK (delivery_state IN (''pending'', ''claimed'', ''acknowledged''))') > 0
    AND instr(COALESCE(sql, ''), '''failed''') = 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'reports') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE report_terminal_schema_guard;
-- +goose StatementEnd
