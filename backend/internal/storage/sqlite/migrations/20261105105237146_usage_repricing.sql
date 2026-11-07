-- +goose Up

-- Priced events by the catalog version that priced them, so a newer version of
-- a provider's rates can find and reprice the rows an older one priced.
CREATE INDEX idx_model_usage_events_priced_version
    ON model_usage_events (pricing_version, id)
    WHERE estimated_cost_nanos IS NOT NULL;

-- One model's events, for repricing after a price override changes.
CREATE INDEX idx_model_usage_events_model
    ON model_usage_events (model_id, id);

-- Sources whose events were stored before the parser recorded a service tier.
-- Each is reread once to fill the tier it reports; a NULL tier also means "none
-- reported", so without this queue the reread could never know it was done.
CREATE TABLE usage_tier_repairs (
    usage_source_id INTEGER PRIMARY KEY REFERENCES usage_sources(id) ON DELETE CASCADE
);

INSERT INTO usage_tier_repairs (usage_source_id)
SELECT DISTINCT event.usage_source_id
FROM model_usage_events event
JOIN usage_sources source ON source.id = event.usage_source_id
WHERE event.service_tier IS NULL
  AND source.kind IN ('claude_main', 'claude_subagent', 'codex_rollout');

-- The user's own prices, by the model ID usage events store. A row holds
-- either its own rates in USD per million tokens (decimal text) or a catalog
-- model to price as.
CREATE TABLE usage_price_overrides (
    model_id TEXT PRIMARY KEY
        CHECK (model_id = trim(model_id) AND model_id <> '' AND length(model_id) <= 256),
    input_usd_per_mtok TEXT,
    output_usd_per_mtok TEXT,
    cache_read_usd_per_mtok TEXT,
    cache_write_usd_per_mtok TEXT,
    price_as TEXT CHECK (price_as IS NULL OR (price_as <> '' AND length(price_as) <= 256)),
    updated_at DATETIME NOT NULL,
    CHECK ((input_usd_per_mtok IS NULL) = (output_usd_per_mtok IS NULL)),
    CHECK ((input_usd_per_mtok IS NULL) <> (price_as IS NULL)),
    CHECK (input_usd_per_mtok IS NOT NULL
           OR (cache_read_usd_per_mtok IS NULL AND cache_write_usd_per_mtok IS NULL))
);

-- +goose Down

DROP TABLE usage_price_overrides;
DROP TABLE usage_tier_repairs;
DROP INDEX idx_model_usage_events_model;
DROP INDEX idx_model_usage_events_priced_version;
