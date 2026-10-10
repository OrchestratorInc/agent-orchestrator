-- Widen the sessions.harness CHECK to allow the Strands adapter.
-- SQLite cannot ALTER a CHECK constraint, so rewrite the sessions schema.
--
-- Every historical harness variant ends with the retained 'fake' fixture
-- harness, so anchoring on that tail widens whichever shape a database arrives
-- with instead of enumerating each one. writable_schema changes run outside a
-- transaction; RESET forces SQLite to reparse the schema.
--
-- usage_bindings carries its own narrower harness CHECK (harnesses whose usage AO
-- can bind to a provider account). Prior harness migrations left it alone and
-- this one does too.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
PRAGMA writable_schema = ON;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sqlite_master
SET sql = replace(sql, '''fake''))', '''strands'', ''fake''))')
WHERE type = 'table' AND name = 'sessions'
  AND sql LIKE '%CHECK (harness IN (%'
  AND sql LIKE '%''prime-agent''%'
  AND sql LIKE '%''omp''%'
  AND sql LIKE '%''unreal-agent''%'
  AND sql LIKE '%''mimo-code''%'
  AND sql LIKE '%''codewhale''%'
  AND sql NOT LIKE '%''strands''%';
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
SET sql = replace(sql, '''strands'', ''fake''))', '''fake''))')
WHERE type = 'table' AND name = 'sessions';
-- +goose StatementEnd
-- +goose StatementBegin
PRAGMA writable_schema = RESET;
-- +goose StatementEnd
