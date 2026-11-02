-- Admit ao_action on conversation_activities.kind.
-- SQLite cannot ALTER a CHECK constraint. Rewriting sqlite_master keeps every
-- existing row, index, and CDC trigger. The tail is unique to this table.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, '''user_input''))', '''user_input'', ''ao_action''))')
WHERE type = 'table' AND name = 'conversation_activities'
  AND sql LIKE '%CHECK (kind IN (%'
  AND sql LIKE '%''user_input''%'
  AND sql NOT LIKE '%''ao_action''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Existing ao_action rows cannot satisfy the previous CHECK. Keep the widened
-- constraint on downgrade, as the earlier activity kind migrations do.
SELECT 1;
-- +goose StatementEnd
