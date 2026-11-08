package usage

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

func TestParsePiUsageBearingEntries(t *testing.T) {
	source := usageSource(domain.UsageSourcePiSession)
	source.NativeRootID = "pi-session"
	records := []jsonlRecord{
		{Data: []byte(`{"type":"session","id":"pi-session","cwd":"/repo","version":3}`)},
		{Offset: 100, Data: []byte(`{"type":"message","id":"assistant-1","timestamp":"2026-08-29T10:00:00Z","message":{"role":"assistant","provider":"openai","model":"requested-model","responseModel":"responding-model","usage":{"input":11,"output":7,"cacheRead":5,"cacheWrite":3,"totalTokens":26,"cost":{"total":0.01}}}}`)},
		{Offset: 200, Data: []byte(`{"type":"message","id":"tool-1","timestamp":"2026-08-29T10:01:00Z","message":{"role":"toolResult","toolName":"nested-agent","usage":{"input":2,"output":1,"cacheRead":4,"cacheWrite":3,"totalTokens":10,"cost":{"total":0.02}}}}`)},
		{Offset: 300, Data: []byte(`{"type":"compaction","id":"compact-1","timestamp":"2026-08-29T10:02:00Z","usage":{"input":6,"output":2,"cacheRead":1,"cacheWrite":1,"totalTokens":10,"cost":{"total":0.03}}}`)},
		{Offset: 400, Data: []byte(`{"type":"branch_summary","id":"branch-1","timestamp":"2026-08-29T10:03:00Z","usage":{"input":8,"output":3,"cacheRead":2,"cacheWrite":1,"totalTokens":14,"cost":{"total":0.04}}}`)},
		{Offset: 500, Data: []byte(`{"type":"message","id":"user-1","message":{"role":"user","content":"ignored"}}`)},
	}

	result := parseRecords(source, records, 600, time.Unix(1700000000, 0).UTC())
	if result.err != nil || len(result.Events) != 4 {
		t.Fatalf("result = %+v", result)
	}
	assistant := result.Events[0]
	if assistant.ProviderID != domain.UsageProviderOpenAI || assistant.BillingProviderID != "openai" ||
		assistant.BillingProviderSource != domain.UsageBillingProviderObserved ||
		assistant.ModelID != "responding-model" || assistant.MeasurementKind != domain.UsageMeasurementNativeReported ||
		assistant.CreatedAt != time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC) ||
		assistant.SourceEventKey == "" || assistant.ProviderUsageJSON == "" {
		t.Fatalf("assistant event = %+v", assistant)
	}
	assertPiTokens(t, assistant.Tokens, 19, 5, 14, 7)
	if providerUsageTokens(t, assistant.ProviderUsageJSON, derivedCacheWriteKey) != 3 {
		t.Fatalf("assistant provider usage = %s", assistant.ProviderUsageJSON)
	}

	wants := []domain.UsageTokenMetrics{
		{InputTokens: int64Ptr(9), CachedInputTokens: int64Ptr(4), UncachedInputTokens: int64Ptr(5), OutputTokens: int64Ptr(1)},
		{InputTokens: int64Ptr(8), CachedInputTokens: int64Ptr(1), UncachedInputTokens: int64Ptr(7), OutputTokens: int64Ptr(2)},
		{InputTokens: int64Ptr(11), CachedInputTokens: int64Ptr(2), UncachedInputTokens: int64Ptr(9), OutputTokens: int64Ptr(3)},
	}
	for index, event := range result.Events[1:] {
		if event.ProviderID != domain.UsageProviderOpenAI || event.BillingProviderID != "" ||
			event.BillingProviderSource != "" || event.ModelID != "Tools/summaries" ||
			event.MeasurementKind != domain.UsageMeasurementNativeReported || event.ProviderUsageJSON == "" ||
			event.SourceEventKey == "" {
			t.Fatalf("unattributed event %d = %+v", index, event)
		}
		assertPiTokenMetricsEqual(t, event.Tokens, wants[index])
	}
}

func TestParsePiAnthropicAssistantUsesResponseModel(t *testing.T) {
	source := usageSource(domain.UsageSourcePiSession)
	source.NativeRootID = "pi-session"
	record := jsonlRecord{Data: []byte(`{"type":"message","id":"assistant-1","message":{"role":"assistant","provider":"anthropic","model":"requested","responseModel":"claude-sonnet","usage":{"input":11,"output":7,"cacheRead":5,"cacheWrite":3,"cacheWrite1h":2,"totalTokens":26}}}`)}

	result := parseRecords(source, []jsonlRecord{record}, 100, time.Now())
	if result.err != nil || len(result.Events) != 1 {
		t.Fatalf("result = %+v", result)
	}
	event := result.Events[0]
	if event.ProviderID != domain.UsageProviderAnthropic || event.BillingProviderID != "anthropic" ||
		event.BillingProviderSource != domain.UsageBillingProviderObserved || event.ModelID != "claude-sonnet" ||
		event.MeasurementKind != domain.UsageMeasurementNativeReported {
		t.Fatalf("event = %+v", event)
	}
	assertPiTokens(t, event.Tokens, 19, 5, 14, 7)
	if providerUsageTokens(t, event.ProviderUsageJSON, derivedCacheWriteKey) != 3 ||
		providerUsageTokens(t, event.ProviderUsageJSON, derivedCacheWrite1HKey) != 2 {
		t.Fatalf("provider usage = %s", event.ProviderUsageJSON)
	}
}

func TestParsePiExplicitUsageEntry(t *testing.T) {
	source := usageSource(domain.UsageSourcePiSession)
	source.NativeRootID = "pi-session"
	records := []jsonlRecord{
		{Offset: 10, Data: []byte(`{"type":"usage","id":"warm-1","kind":"cache_warm","provider":"openai","model":"gpt-test","usage":{"input":4,"output":1,"cacheRead":2,"cacheWrite":1}}`)},
		{Offset: 20, Data: []byte(`{"type":"usage","id":"warm-2","kind":"future_kind","provider":"anthropic","model":"claude-test","usage":{"input":5,"output":2,"cacheRead":3,"cacheWrite":1}}`)},
	}
	result := parseRecords(source, records, 30, time.Now())
	if result.err != nil || len(result.Events) != 2 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	openai, anthropic := result.Events[0], result.Events[1]
	if openai.ProviderID != domain.UsageProviderOpenAI || openai.BillingProviderID != "openai" ||
		openai.ModelID != "gpt-test" || anthropic.ProviderID != domain.UsageProviderAnthropic ||
		anthropic.BillingProviderID != "anthropic" || anthropic.ModelID != "claude-test" ||
		openai.SourceEventKey == anthropic.SourceEventKey {
		t.Fatalf("usage events = %+v", result.Events)
	}
	assertPiTokens(t, openai.Tokens, 7, 2, 5, 1)
	assertPiTokens(t, anthropic.Tokens, 9, 3, 6, 2)
}

func TestParsePiSkipsAssistantWithoutUsageAndFailedRequests(t *testing.T) {
	source := usageSource(domain.UsageSourcePiSession)
	records := []jsonlRecord{
		{Data: []byte(`{"type":"message","id":"interrupted","message":{"role":"assistant","provider":"openai","model":"gpt-test"}}`)},
		{Data: []byte(`{"type":"message","id":"failed","message":{"role":"assistant","provider":"anthropic","model":"claude-test","stopReason":"error","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}`)},
	}
	result := parseRecords(source, records, 100, time.Now())
	if result.err != nil || len(result.Events) != 0 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestParsePiRecordsOtherProvidersWithPiCost(t *testing.T) {
	source := usageSource(domain.UsageSourcePiSession)
	source.NativeRootID = "pi-session"
	records := []jsonlRecord{
		{Offset: 10, Data: []byte(`{"type":"message","id":"zai-1","timestamp":"2026-11-04T02:58:59.526Z","message":{"role":"assistant","provider":"zai-coding-cn","model":"glm-5.3-flash","stopReason":"stop","usage":{"input":4217,"output":99,"cacheRead":0,"cacheWrite":0,"reasoning":55,"totalTokens":4316,"cost":{"input":0.00063255,"output":0.0000495,"cacheRead":0,"cacheWrite":0,"total":0.0006820499999999999}}}}`)},
		{Offset: 20, Data: []byte(`{"type":"message","id":"gemini-1","message":{"role":"assistant","provider":"google","model":"gemini-test","usage":{"input":6,"output":2,"cacheRead":4,"cacheWrite":0}}}`)},
	}
	result := parseRecords(source, records, 30, time.Now())
	if result.err != nil || len(result.Events) != 2 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	zai, gemini := result.Events[0], result.Events[1]
	if zai.ProviderID != domain.UsageProviderOpenAI || zai.BillingProviderID != "zai-coding-cn" ||
		zai.BillingProviderSource != domain.UsageBillingProviderObserved || zai.ModelID != "glm-5.3-flash" {
		t.Fatalf("z.ai event = %+v", zai)
	}
	assertPiTokens(t, zai.Tokens, 4217, 0, 4217, 99)
	costs := zai.Costs
	if costs.PricingVersion != piPricingVersion || costs.EstimatedCostNanos == nil || *costs.EstimatedCostNanos != 682050 ||
		costs.InputCostNanos == nil || *costs.InputCostNanos != 632550 ||
		costs.CachedInputCostNanos == nil || *costs.CachedInputCostNanos != 0 ||
		costs.OutputCostNanos == nil || *costs.OutputCostNanos != 49500 {
		t.Fatalf("z.ai costs = %+v", costs)
	}

	if gemini.ProviderID != domain.UsageProviderOpenAI || gemini.BillingProviderID != "gemini" || gemini.ModelID != "gemini-test" {
		t.Fatalf("gemini event = %+v", gemini)
	}
	assertPiTokens(t, gemini.Tokens, 10, 4, 6, 2)
	if gemini.Costs.EstimatedCostNanos != nil || gemini.Costs.PricingVersion != "" {
		t.Fatalf("gemini without a Pi cost must be left for the catalog: %+v", gemini.Costs)
	}
}

func TestParsePiRejectsInvalidUsage(t *testing.T) {
	source := usageSource(domain.UsageSourcePiSession)
	record := jsonlRecord{Data: []byte(`{"type":"message","id":"bad","message":{"role":"assistant","model":"m","usage":{"input":-1,"output":1}}}`)}
	result := parseRecords(source, []jsonlRecord{record}, 100, time.Now())
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(result.Events) != 0 || result.Cursor.AnomalyCount != 1 ||
		result.Cursor.LastErrorCode != domain.UsageErrorMalformedJSONL {
		t.Fatalf("result = %+v", result)
	}
}

func assertPiTokens(t *testing.T, got domain.UsageTokenMetrics, input, cached, uncached, output int64) {
	t.Helper()
	assertPiTokenMetricsEqual(t, got, domain.UsageTokenMetrics{
		InputTokens: int64Ptr(input), CachedInputTokens: int64Ptr(cached),
		UncachedInputTokens: int64Ptr(uncached), OutputTokens: int64Ptr(output),
	})
}

func assertPiTokenMetricsEqual(t *testing.T, got, want domain.UsageTokenMetrics) {
	t.Helper()
	for name, values := range map[string][2]*int64{
		"input": {got.InputTokens, want.InputTokens}, "cached": {got.CachedInputTokens, want.CachedInputTokens},
		"uncached": {got.UncachedInputTokens, want.UncachedInputTokens}, "output": {got.OutputTokens, want.OutputTokens},
	} {
		if values[0] == nil || values[1] == nil || *values[0] != *values[1] {
			t.Fatalf("%s tokens = %v, want %v", name, values[0], values[1])
		}
	}
}

func TestParsePiNormalizesEveryAPIFamilyAlike(t *testing.T) {
	providers := []string{
		"zai", "openrouter", "deepseek", "kimi-coding", "minimax",
		"amazon-bedrock", "google", "google-vertex", "mistral", "groq",
		"github-copilot", "azure-openai-responses", "my-custom-gateway",
	}
	source := usageSource(domain.UsageSourcePiSession)
	source.NativeRootID = "pi-session"
	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			record := jsonlRecord{Data: []byte(`{"type":"message","id":"a-1","message":{"role":"assistant","provider":"` + provider +
				`","model":"m","usage":{"input":100,"output":40,"cacheRead":30,"cacheWrite":20,"reasoning":15,"totalTokens":190,` +
				`"cost":{"input":0.0001,"output":0.00004,"cacheRead":0.000003,"cacheWrite":0.000025,"total":0.000168}}}}`)}
			result := parseRecords(source, []jsonlRecord{record}, 100, time.Now())
			if result.err != nil || len(result.Events) != 1 || result.Cursor.AnomalyCount != 0 {
				t.Fatalf("result = %+v", result)
			}
			event := result.Events[0]
			if event.ProviderID != domain.UsageProviderOpenAI || event.BillingProviderID != pricing.CanonicalProviderID(provider) ||
				event.BillingProviderSource != domain.UsageBillingProviderObserved {
				t.Fatalf("event = %+v", event)
			}
			assertPiTokens(t, event.Tokens, 150, 30, 120, 40)
			costs := event.Costs
			if costs.EstimatedCostNanos == nil || *costs.EstimatedCostNanos != 168_000 ||
				*costs.InputCostNanos != 125_000 || *costs.CachedInputCostNanos != 3_000 ||
				*costs.OutputCostNanos != 40_000 || costs.PricingVersion != piPricingVersion {
				t.Fatalf("costs = %+v", costs)
			}
		})
	}
}
