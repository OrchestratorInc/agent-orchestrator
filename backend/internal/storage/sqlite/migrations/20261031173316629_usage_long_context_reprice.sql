-- +goose Up
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
SELECT 1;
-- +goose StatementEnd
