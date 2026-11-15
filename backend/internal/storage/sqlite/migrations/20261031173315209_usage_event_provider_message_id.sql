-- +goose Up
ALTER TABLE model_usage_events ADD COLUMN provider_message_id TEXT
    CHECK (provider_message_id IS NULL OR (trim(provider_message_id) <> '' AND length(provider_message_id) <= 256));

CREATE UNIQUE INDEX idx_model_usage_events_provider_message
    ON model_usage_events (provider_id, provider_message_id)
    WHERE provider_message_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_model_usage_events_provider_message;
ALTER TABLE model_usage_events DROP COLUMN provider_message_id;
