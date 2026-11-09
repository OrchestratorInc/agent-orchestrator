-- Add ZCode to the sessions harness constraint without changing usage bindings.
-- ZCode slots in after memcode/hermes and before the retained fake fixture
-- harness. Like the other late harness migrations, the insert is guarded on the
-- constraint already containing the full predecessor set: databases that
-- skipped an earlier harness migration keep their exact historical shape so
-- reconcileHarnessConstraint can repair the whole chain in one pass, and the
-- zcode clause there appends ZCode afterwards.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, '''fake''))', '''zcode'', ''fake''))')
WHERE type = 'table' AND name = 'sessions'
  AND sql LIKE '%CHECK (harness IN (%'
  AND sql LIKE '%''prime-agent''%'
  AND sql LIKE '%''omp''%'
  AND sql LIKE '%''unreal-agent''%'
  AND sql LIKE '%''mimo-code''%'
  AND sql LIKE '%''codewhale''%'
  AND sql LIKE '%''memcode''%'
  AND sql NOT LIKE '%''zcode''%';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd

-- +goose Down
-- Refuse a rollback if ZCode rows would violate the restored CHECK.
-- +goose StatementBegin
CREATE TEMP TABLE IF NOT EXISTS zcode_schema_guard (valid INTEGER NOT NULL CHECK (valid = 1));
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO zcode_schema_guard (valid)
SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM sessions WHERE harness = 'zcode')
    THEN 1 ELSE 0 END;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE zcode_schema_guard;
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, '''zcode'', ''fake''))', '''fake''))')
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
