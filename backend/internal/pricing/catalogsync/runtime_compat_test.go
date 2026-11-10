package catalogsync_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing/catalogsync"
)

// Break caught: the generator accepted fractional trailing zeroes that the
// runtime's stricter canonical decimal decoder rejected.
func TestGeneratedFractionalRatesLoadInRuntime(t *testing.T) {
	upstream := []byte(`{
"anthropic/a":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1},
"openai/o":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":0.0010,"output_cost_per_token":1.2300e-3},
"zai/z":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1}
}`)
	catalog, err := catalogsync.Catalog(upstream)
	if err != nil {
		t.Fatalf("runtime load generated catalog: %v", err)
	}
	one, zero := int64(1), int64(0)
	estimate, err := catalog.Snapshot().Estimate(domain.ModelUsageEvent{
		ProviderID:        domain.UsageProviderOpenAI,
		BillingProviderID: "openai",
		ModelID:           "o",
		MeasurementKind:   domain.UsageMeasurementNativeReported,
		Tokens: domain.UsageTokenMetrics{
			InputTokens: &one, CachedInputTokens: &zero, UncachedInputTokens: &one, OutputTokens: &one,
		},
	})
	if err != nil {
		t.Fatalf("runtime estimate generated rates: %v", err)
	}
	if estimate.TotalNanos == nil || *estimate.TotalNanos != 2_230_000 {
		t.Fatalf("runtime total = %v, want 2230000 nano-USD", estimate.TotalNanos)
	}
}

func TestLiveTableBuildsThroughTheFetcherAndSurvivesCache(t *testing.T) {
	upstream := []byte(`{
"anthropic/claude-sonnet-4":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":0.000003,"output_cost_per_token":0.000015,"cache_read_input_token_cost":0.0000003,"cache_creation_input_token_cost":0.00000375},
"openai/gpt-5":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":0.00000125,"output_cost_per_token":0.00001,"input_cost_per_token_priority":0.0000025,"output_cost_per_token_priority":0.00002},
"zai/glm-4.6":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":0.0000006,"output_cost_per_token":0.0000022}
}`)
	if path := os.Getenv("AO_PRICING_SOURCE"); path != "" {
		var err error
		if upstream, err = os.ReadFile(path); err != nil { //nolint:gosec // CI-supplied path
			t.Fatalf("read AO_PRICING_SOURCE: %v", err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write(upstream)
	}))
	t.Cleanup(server.Close)
	fetcher := catalogsync.NewFetcher(server.Client(), server.URL)
	result, err := fetcher.Fetch(t.Context(), "", false)
	if err != nil {
		t.Fatalf("fetch live catalog: %v", err)
	}
	if result.NotModified || result.Catalog == nil || result.ETag != `"v1"` {
		t.Fatalf("fetch result = %+v, want a complete catalog with its ETag", result)
	}
	fetched := result.Catalog.Snapshot()
	for _, providerID := range pricing.RequiredProviderIDs {
		if fetched.ProviderVersion(providerID) == "" {
			t.Fatalf("fetched catalog has no version for required provider %q", providerID)
		}
	}

	cache := pricing.NewCache(t.TempDir())
	if err := cache.Install(t.Context(), result.Catalog); err != nil {
		t.Fatalf("install fetched catalog: %v", err)
	}
	reloaded, err := cache.Load(t.Context())
	if err != nil {
		t.Fatalf("reload installed catalog: %v", err)
	}
	for _, providerID := range fetched.ProviderIDs() {
		if got, want := reloaded.Snapshot().ProviderVersion(providerID), fetched.ProviderVersion(providerID); got != want {
			t.Errorf("reloaded %s version = %q, want %q", providerID, got, want)
		}
	}

	if cached, err := fetcher.Fetch(t.Context(), `"v1"`, true); err != nil || !cached.NotModified {
		t.Fatalf("conditional fetch with a cache = %+v, %v; want 304", cached, err)
	}
	if cold, err := fetcher.Fetch(t.Context(), `"v1"`, false); err != nil || cold.Catalog == nil {
		t.Fatalf("fetch without a cache = %+v, %v; want the full table", cold, err)
	}
}
