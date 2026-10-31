-- +goose Up
-- Off by default: when on, AO keeps every limits read and reads each account's
-- limits again every five minutes, for the experimental limit windows on the
-- usage page. The app turns it on and off with Developer Mode.
ALTER TABLE app_settings ADD COLUMN limit_history INTEGER NOT NULL DEFAULT 0
    CHECK (limit_history IN (0, 1));

-- +goose Down
ALTER TABLE app_settings DROP COLUMN limit_history;
