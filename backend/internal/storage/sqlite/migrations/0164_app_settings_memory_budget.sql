-- +goose Up
-- +goose StatementBegin
-- The memory budget is what the user is willing to let AO hold; the board's
-- pressure colour is measured against it rather than against the whole
-- machine. Zero means Auto: a quarter of host RAM, clamped to 2–16 GiB.
ALTER TABLE app_settings
    ADD COLUMN memory_budget_bytes INTEGER NOT NULL DEFAULT 0
        CHECK (memory_budget_bytes >= 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_settings DROP COLUMN memory_budget_bytes;
-- +goose StatementEnd
