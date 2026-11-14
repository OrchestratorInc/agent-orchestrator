package catalogsync_test

import (
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing/catalogsync"
)

const everyProviderTable = `{
"anthropic/claude-test":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":0.000003,"output_cost_per_token":0.000015},
"openai/gpt-test":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":0.000001,"output_cost_per_token":0.00001},
"zai/glm-test":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":0.0000006,"output_cost_per_token":0.0000022},
"minimax/MiniMax-M9":{"litellm_provider":"minimax","mode":"chat","input_cost_per_token":0.0000003,"output_cost_per_token":0.0000012,"cache_read_input_token_cost":0.00000003},
"vertex_ai/claude-test@20260101":{"litellm_provider":"vertex_ai-anthropic_models","mode":"chat","input_cost_per_token":0.000003,"output_cost_per_token":0.000015},
"gemini-test":{"litellm_provider":"vertex_ai-language-models","mode":"chat","input_cost_per_token":0.000001,"output_cost_per_token":0.000004},
"bedrock/writer.palmyra-test":{"litellm_provider":"bedrock_converse","mode":"chat","input_cost_per_token":0.000002,"output_cost_per_token":0.000006},
"azure/gpt-test":{"litellm_provider":"azure","mode":"chat","input_cost_per_token":0.000001,"output_cost_per_token":0.00001},
"perplexity/perplexity/doubled":{"litellm_provider":"perplexity","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1},
"perplexity/sonar-test":{"litellm_provider":"perplexity","mode":"chat","input_cost_per_token":0.000001,"output_cost_per_token":0.000001},
"bad/provider":{"litellm_provider":"../escape","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1}
}`

func TestRuntimeCatalogPricesEveryProviderTheTableDoes(t *testing.T) {
	catalog, err := catalogsync.Catalog([]byte(everyProviderTable))
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	snapshot := catalog.Snapshot()
	want := []string{"anthropic", "azure", "bedrock", "minimax", "openai", "perplexity", "vertex_ai", "zai"}
	if got := snapshot.ProviderIDs(); !slices.Equal(got, want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}

	cached, uncached := int64(0), int64(1000)
	input, output := int64(1000), int64(100)
	estimate, err := snapshot.Estimate(domain.ModelUsageEvent{
		ProviderID: domain.UsageProviderOpenAI, BillingProviderID: "minimax", ModelID: "MiniMax-M9",
		Tokens: domain.UsageTokenMetrics{
			InputTokens: &input, CachedInputTokens: &cached, UncachedInputTokens: &uncached, OutputTokens: &output,
		},
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if estimate.TotalNanos == nil || *estimate.TotalNanos != 420_000 {
		t.Fatalf("minimax total = %v, want 420000", estimate.TotalNanos)
	}

	for _, lookup := range []struct{ provider, model string }{
		{"vertex_ai", "claude-test@20260101"},
		{"google-vertex", "gemini-test"},
		{"amazon-bedrock", "writer.palmyra-test"},
	} {
		estimate, err := snapshot.Estimate(domain.ModelUsageEvent{
			ProviderID: domain.UsageProviderOpenAI, BillingProviderID: lookup.provider, ModelID: lookup.model,
			Tokens: domain.UsageTokenMetrics{InputTokens: &input, UncachedInputTokens: &uncached, OutputTokens: &output},
		})
		if err != nil || estimate.InputNanos == nil || estimate.OutputNanos == nil {
			t.Fatalf("%s/%s estimate = %+v, %v; want priced", lookup.provider, lookup.model, estimate, err)
		}
	}
}

func TestProviderForModelIgnoresResellers(t *testing.T) {
	catalog, err := catalogsync.Catalog([]byte(everyProviderTable))
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if got := catalog.Snapshot().ProviderForModel("gpt-test"); got != "openai" {
		t.Fatalf("ProviderForModel(gpt-test) = %q, want openai", got)
	}
	if got := catalog.Snapshot().ProviderForModel("MiniMax-M9"); got != "" {
		t.Fatalf("ProviderForModel(MiniMax-M9) = %q, want no inference", got)
	}
}
