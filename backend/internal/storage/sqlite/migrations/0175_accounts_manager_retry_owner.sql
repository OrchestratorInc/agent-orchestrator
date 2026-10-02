-- +goose Up
ALTER TABLE accounts_manager_switches ADD COLUMN retired_target_generation TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts_manager_switches ADD COLUMN retired_target_handle_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE accounts_manager_switches DROP COLUMN retired_target_handle_id;
ALTER TABLE accounts_manager_switches DROP COLUMN retired_target_generation;
