-- Add Hermes Agent to the sessions harness constraint without changing usage bindings.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''hermes'', ''fake''))')
WHERE type = 'table' AND name = 'sessions' AND sql LIKE '%CHECK (harness IN (%' AND sql LIKE '%''muse''%' AND sql LIKE '%''kimchi''%' AND sql LIKE '%''prime-agent''%' AND sql LIKE '%''omp''%' AND sql LIKE '%''gemini''%' AND sql LIKE '%''unreal-agent''%' AND sql LIKE '%''mimo-code''%' AND sql LIKE '%''codewhale''%' AND sql LIKE '%''deepseek-harness''%' AND sql LIKE '%''openhands''%' AND sql LIKE '%''command-code''%' AND sql NOT LIKE '%''hermes''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
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
