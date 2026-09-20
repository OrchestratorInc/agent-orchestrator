-- +goose Up
-- +goose StatementBegin
-- The memory reserve is how much host RAM the user wants kept free. Below
-- it AO stops auto-starting sessions and says so; it is a warning line, not
-- a cap, so nothing is ever killed. Zero means the default of 2 GiB.
ALTER TABLE app_settings
    ADD COLUMN memory_reserve_bytes INTEGER NOT NULL DEFAULT 0
        CHECK (memory_reserve_bytes >= 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_settings DROP COLUMN memory_reserve_bytes;
-- +goose StatementEnd
