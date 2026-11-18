-- +goose Up
ALTER TABLE model_usage_events ADD COLUMN service_tier TEXT
    CHECK (service_tier IS NULL OR (trim(service_tier) <> '' AND length(service_tier) <= 64));

-- +goose Down
ALTER TABLE model_usage_events DROP COLUMN service_tier;
