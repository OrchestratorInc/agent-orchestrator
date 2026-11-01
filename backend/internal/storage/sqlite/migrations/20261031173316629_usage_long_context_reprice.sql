-- +goose Up
-- OpenAI bills a request with more than 272k input tokens at its model's
-- long-context rates, and AO priced every such request at the base rates. The
-- catalog now carries those rates, so clearing these costs hands the rows to
-- the cost backfiller, which prices them again from the current catalog. An
-- empty pricing_version marks them as priced by no catalog at all, so the
-- backfiller picks them up whatever version is active.
UPDATE model_usage_events
SET input_cost_nanos = NULL,
    cached_input_cost_nanos = NULL,
    output_cost_nanos = NULL,
    estimated_cost_nanos = NULL,
    pricing_version = ''
WHERE provider_id = 'openai'
  AND input_tokens > 272000
  AND billing_provider_id IS NOT NULL;

-- +goose Down
-- +goose StatementBegin
-- The base-rate costs are not kept anywhere, and the backfiller has replaced
-- them with correct ones by now. There is nothing to restore.
SELECT 1;
-- +goose StatementEnd
