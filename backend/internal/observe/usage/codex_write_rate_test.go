package usage

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing/catalogsync"
)

// Break caught: a Codex token_count record may carry only cumulative totals.
// The parser derives the neutral counters from them but used to store the raw
// cumulative object, and the estimator reads its cache-write bucket from the
// optional per-event last_token_usage — so a model that publishes a cache-write
// rate priced its input, and therefore its total, as unknown. Every current
// gpt-5.6 variant publishes one, so this is the models Codex actually runs.
func TestWriteRatedOpenAIModelPricesBothCodexPayloadShapes(t *testing.T) {
	catalog, err := catalogsync.Catalog([]byte(`{
"gpt-5.6":{"litellm_provider":"openai","mode":"responses","input_cost_per_token":0.00000125,"output_cost_per_token":0.00001,
  "cache_read_input_token_cost":0.000000125,"cache_creation_input_token_cost":0.00000125},
"claude-test":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":0.000003,"output_cost_per_token":0.000015},
"glm-test":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":0.0000006,"output_cost_per_token":0.0000022}
}`))
	if err != nil {
		t.Fatalf("build test catalog: %v", err)
	}
	snapshot := catalog.Snapshot()

	const modelID = "gpt-5.6"
	// One logical event, 40 uncached input of which 10 are cache writes,
	// described the two ways Codex is allowed to describe it.
	last := codexTokenVector{
		InputTokens: 100, CachedInputTokens: 60, CacheWriteInputTokens: 10,
		OutputTokens: 20, ReasoningOutputTokens: 5, TotalTokens: 120,
	}
	shapes := map[string][]jsonlRecord{
		"cumulative only": {
			{Offset: 0, Data: []byte(`{"type":"session_meta","payload":{"model_provider":"openai"}}`)},
			{Offset: 100, Data: []byte(`{"type":"turn_context","payload":{"model":` + mustJSONString(modelID) + `}}`)},
			{Offset: 200, Data: codexTokenLine("2026-07-01T10:00:00Z", 100, 60, 10, 20, 5)},
		},
		"with last_token_usage": {
			{Offset: 0, Data: []byte(`{"type":"session_meta","payload":{"model_provider":"openai"}}`)},
			{Offset: 100, Data: []byte(`{"type":"turn_context","payload":{"model":` + mustJSONString(modelID) + `}}`)},
			{Offset: 200, Data: codexTokenLineWithLast("2026-07-01T10:00:00Z", last, last)},
		},
	}

	priced := map[string]pricing.Estimate{}
	for name, records := range shapes {
		result := parseRecords(
			usageSource(domain.UsageSourceCodexRollout), records, 500, time.Unix(1700000000, 0).UTC(),
		)
		if len(result.Events) != 1 {
			t.Fatalf("%s: events = %d, want 1", name, len(result.Events))
		}
		estimate, err := snapshot.Estimate(result.Events[0])
		if err != nil {
			t.Fatalf("%s: estimate: %v", name, err)
		}
		// The write split is what the input charge needs; a model whose
		// rate card is missing some other bucket still leaves the total
		// unknown, and that is the honest answer rather than a defect.
		if estimate.InputNanos == nil {
			t.Fatalf("%s: input cost unknown for a write-rated model (provider usage %s)",
				name, result.Events[0].ProviderUsageJSON)
		}
		priced[name] = estimate
	}
	if !reflect.DeepEqual(priced["cumulative only"], priced["with last_token_usage"]) {
		t.Fatalf("the same event priced differently per payload shape:\n cumulative only:      %+v\n with last_token_usage: %+v",
			priced["cumulative only"], priced["with last_token_usage"])
	}
}

func mustJSONString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
