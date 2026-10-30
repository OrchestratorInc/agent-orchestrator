-- +goose Up
-- An empty map preserves existing installs' model selection after upgrade.
ALTER TABLE app_settings ADD COLUMN harness_defaults TEXT NOT NULL DEFAULT '{}'
    CHECK (json_valid(harness_defaults) AND json_type(harness_defaults) = 'object');

-- +goose Down
ALTER TABLE app_settings DROP COLUMN harness_defaults;
