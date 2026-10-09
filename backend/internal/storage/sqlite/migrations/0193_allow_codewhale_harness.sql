-- Widen the sessions.harness CHECK to allow Codewhale after the shipped
-- OpenCode 2, DeepSeek Harness, MiMo Code, and OpenHands harness migrations.
-- SQLite cannot ALTER a CHECK constraint, so rewrite the sessions schema.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, '''fake''))', '''codewhale'', ''fake''))')
WHERE type = 'table' AND name = 'sessions'
  AND sql LIKE '%CHECK (harness IN (%'
  AND sql LIKE '%''muse''%'
  AND sql LIKE '%''omp''%'
  AND sql LIKE '%''gemini''%'
  AND sql LIKE '%''unreal-agent''%'
  AND sql LIKE '%''mimo-code''%'
  AND sql LIKE '%''deepseek-harness''%'
  AND sql LIKE '%''opencode-v2''%'
  AND sql NOT LIKE '%''codewhale''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, '''codewhale'', ''fake''))', '''fake''))')
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
