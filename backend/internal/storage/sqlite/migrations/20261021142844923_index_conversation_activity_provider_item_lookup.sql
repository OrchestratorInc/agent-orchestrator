-- +goose Up
CREATE INDEX idx_conversation_activities_provider_item_lookup
    ON conversation_activities(provider_item_id)
    WHERE provider_item_id <> '';

-- +goose Down
DROP INDEX idx_conversation_activities_provider_item_lookup;
