-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS schema_app_version (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL CHECK (version >= 0),
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO schema_app_version (id, version)
<<<<<<<< HEAD:backend/internal/storage/sqlite/migrations/0130_schema_app_version.sql
VALUES (1, 130)
========
VALUES (1, 148)
>>>>>>>> c3e355ee4 (fix(storage): avoid schema migration version collision):backend/internal/storage/sqlite/migrations/0148_schema_app_version.sql
ON CONFLICT(id) DO UPDATE SET
    version = excluded.version,
    updated_at = CURRENT_TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS schema_app_version;
-- +goose StatementEnd
