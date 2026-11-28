-- Let an automation belong to the Scratchpad. A NULL project_id names no
-- project: each run starts a standalone worker session, as Scratchpad chats do.
-- SQLite cannot drop NOT NULL in place, so rewrite the automations schema. Every
-- existing row already has a project, and the project foreign key and its
-- cascade are unchanged. writable_schema changes run outside a transaction;
-- RESET forces SQLite to reparse the schema.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, 'project_id    TEXT NOT NULL REFERENCES projects (id)', 'project_id    TEXT REFERENCES projects (id)')
WHERE type = 'table' AND name = 'automations'
    AND instr(sql, 'project_id    TEXT NOT NULL REFERENCES projects (id)') > 0;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS automation_scratchpad_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO automation_scratchpad_schema_guard (valid)
SELECT CASE WHEN instr(COALESCE(sql, ''), 'project_id    TEXT REFERENCES projects (id) ON DELETE CASCADE') > 0 THEN 1 ELSE 0 END
FROM (SELECT (SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'automations') AS sql);
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE automation_scratchpad_schema_guard;
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback while a Scratchpad automation exists, as it would violate the
-- restored NOT NULL, or when the column no longer has a shape this migration
-- knows. NO TRANSACTION means a stop after the rewrite leaves this version
-- applied with NOT NULL already restored, so that shape is accepted too and a
-- retried Down finishes instead of needing a manual repair.
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS automation_scratchpad_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO automation_scratchpad_schema_guard (valid)
SELECT CASE WHEN EXISTS (SELECT 1 FROM automations WHERE project_id IS NULL)
    OR NOT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'automations'
        AND (instr(sql, 'project_id    TEXT REFERENCES projects (id)') > 0
            OR instr(sql, 'project_id    TEXT NOT NULL REFERENCES projects (id)') > 0))
    THEN 0 ELSE 1 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE automation_scratchpad_schema_guard;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, 'project_id    TEXT REFERENCES projects (id)', 'project_id    TEXT NOT NULL REFERENCES projects (id)')
WHERE type = 'table' AND name = 'automations'
    AND instr(sql, 'project_id    TEXT REFERENCES projects (id)') > 0;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
