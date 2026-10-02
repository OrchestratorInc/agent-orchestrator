-- +goose Up
ALTER TABLE accounts_manager_switches ADD COLUMN empty_source INTEGER NOT NULL DEFAULT 0 CHECK (empty_source IN (0, 1));
ALTER TABLE accounts_manager_switches ADD COLUMN source_native_conversation_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE accounts_manager_switches DROP COLUMN source_native_conversation_id;
ALTER TABLE accounts_manager_switches DROP COLUMN empty_source;
