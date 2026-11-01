-- +goose Up
-- The provider's own word for the tier that served a request: Codex's thread
-- service_tier ("default", "priority") or Claude's usage speed ("standard",
-- "fast"). Priority and fast requests bill at a multiple of standard rates, so
-- pricing needs the tier. NULL means nothing reported one, which prices at
-- standard rates; every row stored before this column is NULL.
ALTER TABLE model_usage_events ADD COLUMN service_tier TEXT
    CHECK (service_tier IS NULL OR (trim(service_tier) <> '' AND length(service_tier) <= 64));

-- +goose Down
ALTER TABLE model_usage_events DROP COLUMN service_tier;
