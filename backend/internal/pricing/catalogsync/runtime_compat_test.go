package catalogsync_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing/catalogsync"
)

// Break caught: the generator accepted fractional trailing zeroes that the
// runtime's stricter canonical decimal decoder rejected.
func TestGeneratedFractionalRatesLoadInRuntime(t *testing.T) {
	root := t.TempDir()
	upstream := []byte(`{
"anthropic/a":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1},
"openai/o":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":0.0010,"output_cost_per_token":1.2300e-3},
"zai/z":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1}
}`)
	_, err := catalogsync.Sync(root, upstream, catalogsync.Source{
		Repository: "BerriAI/litellm",
		Revision:   "0123456789abcdef0123456789abcdef01234567",
		Path:       "model_prices_and_context_window.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := pricing.NewCache(root).Load(t.Context())
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

// TestLiveBuildMatchesGeneratedCatalogAndSurvivesCache checks the daemon's
// catalog path end to end: LiteLLM table -> Fetcher -> last-known-good cache.
// The table builds to the same provider versions as the repository catalog
// Sync writes from it, so released builds that still download that catalog
// price exactly what the daemon prices. A 304 stands in for the table only
// when a cached catalog exists to serve.
//
// Set AO_PRICING_SOURCE to a LiteLLM table to run the same contract against
// it, as CI does with the live one; unset, the test uses a small fixture.
func TestLiveBuildMatchesGeneratedCatalogAndSurvivesCache(t *testing.T) {
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
	root := t.TempDir()
	if _, err := catalogsync.Sync(root, upstream, catalogsync.Source{
		Repository: "BerriAI/litellm",
		Revision:   "0123456789abcdef0123456789abcdef01234567",
		Path:       "model_prices_and_context_window.json",
	}); err != nil {
		t.Fatalf("generate catalog: %v", err)
	}
	generated, err := pricing.NewCache(root).Load(t.Context())
	if err != nil {
		t.Fatalf("load generated catalog from disk: %v", err)
	}
	providerIDs := manifestProviderIDs(t, filepath.Join(root, "pricing", "catalog", "v1", "manifest.json"))
	if len(providerIDs) == 0 {
		t.Fatal("generated manifest lists no providers")
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

	cache := pricing.NewCache(t.TempDir())
	if err := cache.Install(t.Context(), result.Catalog); err != nil {
		t.Fatalf("install fetched catalog: %v", err)
	}
	reloaded, err := cache.Load(t.Context())
	if err != nil {
		t.Fatalf("reload installed catalog: %v", err)
	}
	for _, providerID := range providerIDs {
		want := generated.Snapshot().ProviderVersion(providerID)
		if want == "" {
			t.Fatalf("generated catalog has no version for provider %q", providerID)
		}
		if got := result.Catalog.Snapshot().ProviderVersion(providerID); got != want {
			t.Errorf("fetched %s version = %q, want %q", providerID, got, want)
		}
		if got := reloaded.Snapshot().ProviderVersion(providerID); got != want {
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

func manifestProviderIDs(t *testing.T, path string) []string {
	t.Helper()
	contents, err := os.ReadFile(path) //nolint:gosec // test-controlled generated path
	if err != nil {
		t.Fatalf("read generated manifest: %v", err)
	}
	var manifest struct {
		Providers []struct {
			ProviderID string `json:"providerId"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatalf("decode generated manifest: %v", err)
	}
	ids := make([]string, 0, len(manifest.Providers))
	for _, provider := range manifest.Providers {
		ids = append(ids, provider.ProviderID)
	}
	return ids
}
