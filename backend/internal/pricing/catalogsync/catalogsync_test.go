package catalogsync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Break caught: emitting raw upstream names, floating-point spellings, or a
// provider blob whose address is not its exact canonical JSON bytes.
func TestBuildEmitsCanonicalContentAddressedCatalog(t *testing.T) {
	upstream := []byte(`{
  "OpenAI/GPT-4o": {
    "litellm_provider": " OpenAI ",
    "mode": "responses",
    "input_cost_per_token": 1e-6,
    "output_cost_per_token": 0,
    "cache_read_input_token_cost": 0
  },
  "zai/GLM-4.5": {
    "litellm_provider": "z.ai",
    "mode": "chat",
    "input_cost_per_token": 0.000002,
    "output_cost_per_token": 0.000004
  },
  "anthropic/claude-test": {
    "litellm_provider": "anthropic",
    "mode": "chat",
    "input_cost_per_token": 0.000003,
    "output_cost_per_token": 0.000015,
    "cache_creation_input_token_cost": 0.00000375,
    "cache_creation_input_token_cost_above_1hr": 0.000006
  }
}`)

	generated, err := build(upstream)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := canonicalJSON(generated.manifest)
	if err != nil {
		t.Fatal(err)
	}
	wantManifest := `{"schemaVersion":1,"source":{"repository":"BerriAI/litellm","revision":"%s","path":"model_prices_and_context_window.json"},"providers":[{"providerId":"anthropic","version":"ao-catalog:anthropic:sha256:030ece6928c2c0e57313e57395c506889aecfa047fb295f38832f0424c16dd2a","sha256":"030ece6928c2c0e57313e57395c506889aecfa047fb295f38832f0424c16dd2a","path":"providers/anthropic/030ece6928c2c0e57313e57395c506889aecfa047fb295f38832f0424c16dd2a.json","modelCount":1},{"providerId":"openai","version":"ao-catalog:openai:sha256:893e629ba2ea7af7f13466a95a07d60ba2506a9bdef72aa6380c6daa7332769d","sha256":"893e629ba2ea7af7f13466a95a07d60ba2506a9bdef72aa6380c6daa7332769d","path":"providers/openai/893e629ba2ea7af7f13466a95a07d60ba2506a9bdef72aa6380c6daa7332769d.json","modelCount":1},{"providerId":"zai","version":"ao-catalog:zai:sha256:6945a8b33e7133ba6c9ce96378ef834ecab1f9c9872b5f5d772a2e1b3fb8805a","sha256":"6945a8b33e7133ba6c9ce96378ef834ecab1f9c9872b5f5d772a2e1b3fb8805a","path":"providers/zai/6945a8b33e7133ba6c9ce96378ef834ecab1f9c9872b5f5d772a2e1b3fb8805a.json","modelCount":1}]}
`
	wantManifest = fmt.Sprintf(wantManifest, gitBlobID(upstream))
	if string(manifest) != wantManifest {
		t.Fatalf("manifest = %s, want %s", manifest, wantManifest)
	}

	openAI := generated.blobs["openai"]
	wantOpenAI := `{"schemaVersion":1,"providerId":"openai","models":[{"modelId":"gpt-4o","rates":{"uncachedInputUsdPerToken":"0.000001","cacheReadUsdPerToken":"0","outputUsdPerToken":"0"}}]}
`
	if string(openAI) != wantOpenAI {
		t.Fatalf("OpenAI blob = %s, want %s", openAI, wantOpenAI)
	}
}

// Break caught: treating conflicting provider/model records as independent
// models would silently select an arbitrary price.
func TestBuildDropsConflictingDuplicatesAndUnreadableEntries(t *testing.T) {
	upstream := `{
"openai/gpt-test":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":2},
"OPENAI/GPT-TEST":{"litellm_provider":" OPENAI ","mode":"responses","input_cost_per_token":3,"output_cost_per_token":2},
"openai/unreadable":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":"cheap","output_cost_per_token":2},
"other/unreadable":{"litellm_provider":7},
"openai/ok":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":2},
"anthropic/a":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1},
"zai/z":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1}
}`
	var ids []string
	for _, model := range buildBlobs(t, []byte(upstream))["openai"].Models {
		ids = append(ids, model.ModelID)
	}
	if strings.Join(ids, ",") != "ok" {
		t.Fatalf("openai models = %v, want only ok", ids)
	}
}

// Break caught: accepting unsupported LiteLLM variants as base prices, or
// allowing a reviewed provider to disappear without failing the catalog build.
func TestBuildFiltersUnsupportedRecordsAndRequiresEveryProvider(t *testing.T) {
	upstream := `{
"anthropic/a":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1},
"openai/o":{"litellm_provider":"openai","mode":"embedding","input_cost_per_token":1,"output_cost_per_token":1},
"zai/z":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1},
"other/x":{"litellm_provider":"other","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1}
}`
	_, err := build([]byte(upstream))
	if err == nil || !strings.Contains(err.Error(), `provider "openai" produced zero supported models`) {
		t.Fatalf("build error = %v, want missing openai provider error", err)
	}
}

// Break caught: a metadata-only LiteLLM entry preventing the catalog from
// including another, fully priced model from the same reviewed provider.
func TestBuildIgnoresSupportedModeRecordsWithoutBaseRates(t *testing.T) {
	upstream := []byte(`{
"anthropic/a":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1},
"openai/metadata-only":{"litellm_provider":"openai","mode":"chat"},
"openai/o":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1},
"zai/z":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1}
}`)
	if _, err := build(upstream); err != nil {
		t.Fatalf("build returned %v for an unpriced metadata entry", err)
	}
}

// Break caught: decimal and scientific whole-number rates emitting a trailing
// decimal point, then making the generated catalog fail its own validator.
func TestBuildCanonicalizesWholeDecimalAndScientificRates(t *testing.T) {
	upstream := []byte(`{
"anthropic/a":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1},
"openai/o":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":1.0,"output_cost_per_token":2e0},
"zai/z":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1}
}`)
	blob := buildBlobs(t, upstream)["openai"]
	if got := blob.Models[0].Rates.UncachedInputUSDPerToken; got != "1" {
		t.Fatalf("input rate = %q, want %q", got, "1")
	}
	if got := blob.Models[0].Rates.OutputUSDPerToken; got != "2" {
		t.Fatalf("output rate = %q, want %q", got, "2")
	}
}

// Break caught: LiteLLM names Anthropic's one-hour cache-creation price with
// the above_1hr suffix. Reading a made-up key silently drops the rate.
func TestBuildMapsLiteLLMAnthropicOneHourCacheCreationRate(t *testing.T) {
	upstream := []byte(`{
"anthropic/a":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1,"cache_creation_input_token_cost_above_1hr":0.000006},
"openai/o":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1},
"zai/z":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1}
}`)
	blob := buildBlobs(t, upstream)["anthropic"]
	if got := blob.Models[0].Rates.CacheWrite1HUSDPerToken; got == nil || *got != "0.000006" {
		t.Fatalf("one-hour cache write rate = %v, want 0.000006", got)
	}
}

// Break caught: sub-one-dollar decimal spellings kept insignificant trailing
// zeroes, producing blobs rejected by the stricter runtime decoder.
func TestNormalizeDecimalRemovesFractionalTrailingZeroes(t *testing.T) {
	for input, want := range map[string]string{
		"0.0010":     "0.001",
		"1.2300e-3":  "0.00123",
		"0.00000000": "0",
	} {
		got, err := normalizeDecimal(input)
		if err != nil {
			t.Fatalf("normalizeDecimal(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("normalizeDecimal(%q) = %q, want %q", input, got, want)
		}
	}
}

// Break caught: identical optional cache prices used distinct string pointers,
// so canonical duplicate records were incorrectly reported as conflicting.
func TestBuildDeduplicatesIdenticalOptionalRates(t *testing.T) {
	upstream := []byte(`{
"anthropic/a":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1},
"openai/gpt-test":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":2,"cache_read_input_token_cost":0.5,"cache_creation_input_token_cost":1.25},
"OPENAI/GPT-TEST":{"litellm_provider":" OPENAI ","mode":"responses","input_cost_per_token":1,"output_cost_per_token":2,"cache_read_input_token_cost":0.5,"cache_creation_input_token_cost":1.25},
"zai/z":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":1,"output_cost_per_token":1}
}`)
	blob := buildBlobs(t, upstream)["openai"]
	if got := len(blob.Models); got != 1 {
		t.Fatalf("OpenAI model count = %d, want 1", got)
	}
}

// Break caught: trusting an in-range int64 exponent could make normalization
// panic or attempt an enormous allocation while expanding scientific notation.
func TestNormalizeDecimalRejectsUnboundedScientificExponent(t *testing.T) {
	const helperEnv = "AO_CATALOGSYNC_HUGE_EXPONENT_HELPER"
	if os.Getenv(helperEnv) == "1" {
		if _, err := normalizeDecimal("1e9223372036854775807"); err == nil {
			os.Exit(2)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNormalizeDecimalRejectsUnboundedScientificExponent$")
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("normalization did not reject the huge exponent promptly: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("normalization panicked or failed its helper process: %v\n%s", err, output)
	}
}

func buildBlobs(t *testing.T, upstream []byte) map[string]providerBlob {
	t.Helper()
	generated, err := build(upstream)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Catalog(upstream); err != nil {
		t.Fatalf("runtime rejected the built catalog: %v", err)
	}
	blobs := make(map[string]providerBlob, len(generated.blobs))
	for providerID, contents := range generated.blobs {
		var blob providerBlob
		if err := json.Unmarshal(contents, &blob); err != nil {
			t.Fatal(err)
		}
		blobs[providerID] = blob
	}
	return blobs
}

// Break caught: the feed carried an Anthropic 1-hour cache-write rate belonging
// to a different model — 5x too low for claude-3-opus-20240229, 12x too high
// for claude-3-haiku-20240307. Canonical-decimal validation accepted both, so
// the daily sync shipped them and every 1-hour cache write on those models was
// billed against a number Anthropic never charged.
func TestBuildDropsImplausibleAnthropicCacheTiers(t *testing.T) {
	upstream := []byte(`{
  "anthropic/one-hour-below-five-minute": {"litellm_provider":"anthropic","mode":"chat",
    "input_cost_per_token":0.000015,"output_cost_per_token":0.000075,
    "cache_creation_input_token_cost":0.00001875,
    "cache_creation_input_token_cost_above_1hr":0.000006},
  "anthropic/one-hour-far-above-input": {"litellm_provider":"anthropic","mode":"chat",
    "input_cost_per_token":0.00000025,"output_cost_per_token":0.00000125,
    "cache_creation_input_token_cost":0.0000003,
    "cache_creation_input_token_cost_above_1hr":0.000006},
  "anthropic/plausible": {"litellm_provider":"anthropic","mode":"chat",
    "input_cost_per_token":0.000005,"output_cost_per_token":0.000025,
    "cache_creation_input_token_cost":0.00000625,
    "cache_creation_input_token_cost_above_1hr":0.00001},
  "zai/free-cache-write": {"litellm_provider":"zai","mode":"chat",
    "input_cost_per_token":0.0000006,"output_cost_per_token":0.000002,
    "cache_creation_input_token_cost":0},
  "openai/discounted-cache-write": {"litellm_provider":"openai","mode":"chat",
    "input_cost_per_token":0.00000375,"output_cost_per_token":0.000015,
    "cache_creation_input_token_cost":0.000001875}
}`)
	blobs := buildBlobs(t, upstream)
	anthropic := blobs["anthropic"]
	for _, test := range []struct {
		modelID      string
		wantWrite    string
		wantWrite1H  string
		wantSurvival string
	}{
		// The 5-minute tier is sound in both, so only the hour is dropped: an
		// absent rate prices that bucket as unknown, which is the honest answer.
		{modelID: "one-hour-below-five-minute", wantWrite: "0.00001875", wantSurvival: "5m survives"},
		{modelID: "one-hour-far-above-input", wantWrite: "0.0000003", wantSurvival: "5m survives"},
		{modelID: "plausible", wantWrite: "0.00000625", wantWrite1H: "0.00001", wantSurvival: "both survive"},
	} {
		modelRates := blobRates(t, anthropic, test.modelID)
		if got := optionalRateString(modelRates.CacheWriteUSDPerToken); got != test.wantWrite {
			t.Errorf("%s (%s) 5m write = %q, want %q", test.modelID, test.wantSurvival, got, test.wantWrite)
		}
		if got := optionalRateString(modelRates.CacheWrite1HUSDPerToken); got != test.wantWrite1H {
			t.Errorf("%s (%s) 1h write = %q, want %q", test.modelID, test.wantSurvival, got, test.wantWrite1H)
		}
	}

	// The relation is Anthropic's, not a universal law. z.ai writes to cache for
	// free and an OpenAI fine-tune writes at half its input rate; deleting those
	// would be the same class of error in the other direction.
	zai := blobRates(t, blobs["zai"], "free-cache-write")
	if got := optionalRateString(zai.CacheWriteUSDPerToken); got != "0" {
		t.Errorf("zai free cache write = %q, want it kept", got)
	}
	openai := blobRates(t, blobs["openai"], "discounted-cache-write")
	if got := optionalRateString(openai.CacheWriteUSDPerToken); got != "0.000001875" {
		t.Errorf("openai discounted cache write = %q, want it kept", got)
	}
}

func blobRates(t *testing.T, blob providerBlob, modelID string) rates {
	t.Helper()
	for _, model := range blob.Models {
		if model.ModelID == modelID {
			return model.Rates
		}
	}
	t.Fatalf("model %q missing from %s blob", modelID, blob.ProviderID)
	return rates{}
}

func optionalRateString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func TestBuildEmitsCompleteLongContextRows(t *testing.T) {
	upstream := []byte(`{
  "gpt-test": {
    "litellm_provider": "openai",
    "mode": "responses",
    "input_cost_per_token": 2e-6,
    "output_cost_per_token": 1e-5,
    "cache_read_input_token_cost": 2e-7,
    "input_cost_per_token_priority": 4e-6,
    "output_cost_per_token_priority": 2e-5,
    "cache_read_input_token_cost_priority": 4e-7,
    "input_cost_per_token_above_272k_tokens": 4e-6,
    "output_cost_per_token_above_272k_tokens": 1.5e-5,
    "cache_read_input_token_cost_above_272k_tokens": 4e-7,
    "input_cost_per_token_above_272k_tokens_priority": 8e-6,
    "output_cost_per_token_above_272k_tokens_priority": 3e-5,
    "cache_read_input_token_cost_above_272k_tokens_priority": 8e-7
  },
  "gpt-gap": {
    "litellm_provider": "openai",
    "mode": "responses",
    "input_cost_per_token": 2e-6,
    "output_cost_per_token": 1e-5,
    "cache_read_input_token_cost": 2e-7,
    "input_cost_per_token_above_272k_tokens": 4e-6,
    "output_cost_per_token_above_272k_tokens": 1.5e-5
  },
  "glm-test": {"litellm_provider": "zai", "mode": "chat", "input_cost_per_token": 1e-6, "output_cost_per_token": 2e-6},
  "claude-test": {"litellm_provider": "anthropic", "mode": "chat", "input_cost_per_token": 3e-6, "output_cost_per_token": 1.5e-5}
}`)
	openai := buildBlobs(t, upstream)["openai"]
	var ids []string
	for _, model := range openai.Models {
		ids = append(ids, model.ModelID)
	}
	want := "gpt-gap,gpt-test,gpt-test#above272k,gpt-test#priority,gpt-test#priority#above272k"
	if got := strings.Join(ids, ","); got != want {
		t.Fatalf("openai models = %s, want %s", got, want)
	}
	long := blobRates(t, openai, "gpt-test#above272k")
	if long.UncachedInputUSDPerToken != "0.000004" || long.OutputUSDPerToken != "0.000015" ||
		optionalRateString(long.CacheReadUSDPerToken) != "0.0000004" {
		t.Fatalf("long-context rates = %+v", long)
	}
	priority := blobRates(t, openai, "gpt-test#priority#above272k")
	if priority.UncachedInputUSDPerToken != "0.000008" || priority.OutputUSDPerToken != "0.00003" ||
		optionalRateString(priority.CacheReadUSDPerToken) != "0.0000008" {
		t.Fatalf("priority long-context rates = %+v", priority)
	}
}

func TestBuildEmitsCompleteServiceTierRows(t *testing.T) {
	upstream := []byte(`{
  "gpt-test": {
    "litellm_provider": "openai",
    "mode": "responses",
    "input_cost_per_token": 2e-6,
    "output_cost_per_token": 1e-5,
    "cache_read_input_token_cost": 2e-7,
    "input_cost_per_token_priority": 4e-6,
    "output_cost_per_token_priority": 2e-5,
    "cache_read_input_token_cost_priority": 4e-7,
    "input_cost_per_token_flex": 1e-6,
    "output_cost_per_token_flex": 5e-6
  },
  "glm-test": {
    "litellm_provider": "zai",
    "mode": "chat",
    "input_cost_per_token": 0.000001,
    "output_cost_per_token": 0.000002
  },
  "claude-test": {
    "litellm_provider": "anthropic",
    "mode": "chat",
    "input_cost_per_token": 0.000003,
    "output_cost_per_token": 0.000015,
    "cache_read_input_token_cost": 3e-7,
    "cache_creation_input_token_cost": 0.00000375,
    "cache_creation_input_token_cost_above_1hr": 0.000006,
    "provider_specific_entry": {"us": 1.1, "fast": 2}
  }
}`)
	blobs := buildBlobs(t, upstream)
	openai := blobs["openai"]
	var openaiIDs []string
	for _, model := range openai.Models {
		openaiIDs = append(openaiIDs, model.ModelID)
	}
	if got, want := strings.Join(openaiIDs, ","), "gpt-test,gpt-test#priority"; got != want {
		t.Fatalf("openai models = %s, want %s", got, want)
	}
	priority := blobRates(t, openai, "gpt-test#priority")
	if priority.UncachedInputUSDPerToken != "0.000004" || priority.OutputUSDPerToken != "0.00002" ||
		optionalRateString(priority.CacheReadUSDPerToken) != "0.0000004" {
		t.Fatalf("priority rates = %+v", priority)
	}

	fast := blobRates(t, blobs["anthropic"], "claude-test#fast")
	got := []string{
		fast.UncachedInputUSDPerToken, fast.OutputUSDPerToken,
		optionalRateString(fast.CacheReadUSDPerToken),
		optionalRateString(fast.CacheWriteUSDPerToken),
		optionalRateString(fast.CacheWrite1HUSDPerToken),
	}
	want := []string{"0.000006", "0.00003", "0.0000006", "0.0000075", "0.000012"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("fast rates = %v, want %v", got, want)
	}
}
