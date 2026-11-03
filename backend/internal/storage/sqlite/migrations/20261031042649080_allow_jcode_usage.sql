-- Let usage bindings record jcode sessions and their usage ledger sources.
-- Widens the two CHECK lists in place, as the harness constraint migrations do.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''codex'', ''kimi''))', '''codex'', ''kimi'', ''jcode''))')
WHERE type = 'table' AND name = 'usage_bindings' AND sql LIKE '%''codex'', ''kimi''))%' AND sql NOT LIKE '%''jcode''%';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''kimi_wire''))', '''kimi_wire'', ''jcode_ledger''))')
WHERE type = 'table' AND name = 'usage_sources' AND sql LIKE '%''kimi_wire''))%' AND sql NOT LIKE '%''jcode_ledger''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS jcode_usage_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO jcode_usage_schema_guard (valid)
SELECT CASE WHEN instr((SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'usage_bindings'), '''jcode''') > 0
    AND instr((SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'usage_sources'), '''jcode_ledger''') > 0
    THEN 1 ELSE 0 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE jcode_usage_schema_guard;
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback while jcode usage rows exist.
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS jcode_usage_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO jcode_usage_schema_guard (valid)
SELECT CASE WHEN EXISTS (SELECT 1 FROM usage_bindings WHERE harness = 'jcode')
    OR EXISTS (SELECT 1 FROM usage_sources WHERE kind = 'jcode_ledger') THEN 0 ELSE 1 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE jcode_usage_schema_guard;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, ', ''jcode''))', '))')
WHERE type = 'table' AND name = 'usage_bindings' AND sql LIKE '%''kimi'', ''jcode''))%';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, ', ''jcode_ledger''))', '))')
WHERE type = 'table' AND name = 'usage_sources' AND sql LIKE '%''kimi_wire'', ''jcode_ledger''))%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
