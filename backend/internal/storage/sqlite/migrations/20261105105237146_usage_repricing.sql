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

-- +goose Down

DROP TABLE usage_tier_repairs;
DROP INDEX idx_model_usage_events_model;
DROP INDEX idx_model_usage_events_priced_version;
