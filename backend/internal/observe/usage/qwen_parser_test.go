package usage

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestParseQwenUsageForBoundSession(t *testing.T) {
	source := usageSource(domain.UsageSourceQwenMonthly)
	source.NativeRootID = "qwen-session"
	records := []jsonlRecord{
		{Data: []byte(`{"schemaVersion":1,"id":"other","sessionId":"another","model":"qwen3","inputTokens":999,"outputTokens":999,"cachedTokens":0,"thoughtsTokens":0,"totalTokens":1998}`)},
		{Offset: 100, Data: []byte(`{"schemaVersion":1,"id":"turn-1","timestamp":"2026-08-09T10:00:00Z","sessionId":"qwen-session","model":"qwen3-coder","authType":"gemini","inputTokens":30,"outputTokens":12,"cachedTokens":9,"thoughtsTokens":4,"totalTokens":46}`)},
	}

	result := parseRecords(source, records, 300, time.Unix(1700000000, 0).UTC())
	if result.err != nil || len(result.Events) != 1 {
		t.Fatalf("result = %+v", result)
	}
	event := result.Events[0]
	if event.ProviderID != domain.UsageProviderOpenAI || event.ModelID != "qwen3-coder" ||
		event.CreatedAt != time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC) || event.SourceEventKey == "" {
		t.Fatalf("event = %+v", event)
	}
	if tokenValue(event.Tokens.InputTokens) != 30 ||
		tokenValue(event.Tokens.CachedInputTokens) != 9 ||
		tokenValue(event.Tokens.UncachedInputTokens) != 21 ||
		tokenValue(event.Tokens.OutputTokens) != 16 {
		t.Fatalf("tokens = %+v", event.Tokens)
	}
	if event.MeasurementKind != domain.UsageMeasurementNativeReported {
		t.Fatalf("measurement kind = %q, want native_reported", event.MeasurementKind)
	}
	if providerUsageTokens(t, event.ProviderUsageJSON, "thoughtsTokens") != 4 ||
		providerUsageTokens(t, event.ProviderUsageJSON, "totalTokens") != 46 {
		t.Fatalf("provider usage = %s", event.ProviderUsageJSON)
	}
}

func TestParseQwenRejectsCachedTokensExceedingInput(t *testing.T) {
	source := usageSource(domain.UsageSourceQwenMonthly)
	source.NativeRootID = "native-root"
	record := jsonlRecord{Data: []byte(`{"schemaVersion":1,"id":"bad","sessionId":"native-root","model":"qwen3","inputTokens":3,"outputTokens":2,"cachedTokens":4,"thoughtsTokens":0,"totalTokens":5}`)}
	result := parseRecords(source, []jsonlRecord{record}, 100, time.Now())
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(result.Events) != 0 || result.Cursor.AnomalyCount != 1 ||
		result.Cursor.LastErrorCode != domain.UsageErrorMalformedJSONL {
		t.Fatalf("result = %+v", result)
	}
}

func TestParseQwenKeepsProviderReportedTotal(t *testing.T) {
	source := usageSource(domain.UsageSourceQwenMonthly)
	source.NativeRootID = "native-root"
	record := jsonlRecord{Data: []byte(`{"schemaVersion":1,"id":"turn-2","sessionId":"native-root","model":"qwen3","inputTokens":3,"outputTokens":2,"cachedTokens":1,"thoughtsTokens":0,"totalTokens":9}`)}
	result := parseRecords(source, []jsonlRecord{record}, 100, time.Now())
	if result.err != nil || len(result.Events) != 1 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	if providerUsageTokens(t, result.Events[0].ProviderUsageJSON, "totalTokens") != 9 {
		t.Fatalf("provider usage = %s", result.Events[0].ProviderUsageJSON)
	}
}

func TestParseQwenAllowsOmittedTotalTokens(t *testing.T) {
	source := usageSource(domain.UsageSourceQwenMonthly)
	source.NativeRootID = "native-root"
	record := jsonlRecord{Data: []byte(`{"schemaVersion":1,"id":"turn-1","sessionId":"native-root","model":"qwen3","inputTokens":3,"outputTokens":2,"cachedTokens":1,"thoughtsTokens":0}`)}
	result := parseRecords(source, []jsonlRecord{record}, 100, time.Now())
	if result.err != nil || len(result.Events) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestParseQwenCountsThoughtsOnceByAuthType(t *testing.T) {
	source := usageSource(domain.UsageSourceQwenMonthly)
	source.NativeRootID = "qwen-session"
	cases := []struct {
		name, record string
		output       int64
	}{
		{"openai", `{"schemaVersion":1,"id":"a","sessionId":"qwen-session","model":"glm-5.3","authType":"openai","source":"managed-auto-memory-extractor","inputTokens":5697,"outputTokens":254,"cachedTokens":0,"thoughtsTokens":191,"totalTokens":5951}`, 254},
		{"openai-responses", `{"schemaVersion":1,"id":"b","sessionId":"qwen-session","model":"gpt-test","authType":"openai-responses","inputTokens":100,"outputTokens":40,"cachedTokens":10,"thoughtsTokens":15,"totalTokens":140}`, 40},
		{"qwen-oauth", `{"schemaVersion":1,"id":"c","sessionId":"qwen-session","model":"coder-model","authType":"qwen-oauth","inputTokens":100,"outputTokens":40,"cachedTokens":0,"thoughtsTokens":15,"totalTokens":140}`, 40},
		{"anthropic", `{"schemaVersion":1,"id":"d","sessionId":"qwen-session","model":"claude-test","authType":"anthropic","inputTokens":100,"outputTokens":40,"cachedTokens":20,"thoughtsTokens":0,"totalTokens":140}`, 40},
		{"vertex-ai", `{"schemaVersion":1,"id":"e","sessionId":"qwen-session","model":"gemini-test","authType":"vertex-ai","inputTokens":100,"outputTokens":40,"cachedTokens":0,"thoughtsTokens":15,"totalTokens":155}`, 55},
		{"unknown, total counts thoughts apart", `{"schemaVersion":1,"id":"f","sessionId":"qwen-session","model":"m","authType":"unknown","inputTokens":100,"outputTokens":40,"cachedTokens":0,"thoughtsTokens":15,"totalTokens":155}`, 55},
		{"unknown, total counts thoughts inside", `{"schemaVersion":1,"id":"g","sessionId":"qwen-session","model":"m","authType":"unknown","inputTokens":100,"outputTokens":40,"cachedTokens":0,"thoughtsTokens":15,"totalTokens":140}`, 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseRecords(source, []jsonlRecord{{Data: []byte(tc.record)}}, 100, time.Now())
			if result.err != nil || len(result.Events) != 1 || result.Cursor.AnomalyCount != 0 {
				t.Fatalf("result = %+v", result)
			}
			if got := tokenValue(result.Events[0].Tokens.OutputTokens); got != tc.output {
				t.Fatalf("output = %d, want %d", got, tc.output)
			}
		})
	}
}

func TestParseQwenBillsTheResolvedRoute(t *testing.T) {
	record := jsonlRecord{Data: []byte(`{"schemaVersion":1,"id":"turn-1","sessionId":"qwen-session","model":"glm-5.3","authType":"openai","inputTokens":18472,"outputTokens":3,"cachedTokens":0,"thoughtsTokens":0,"totalTokens":18475}`)}
	for _, tc := range []struct{ hint, want string }{
		{"zai", "zai"}, {"Z.ai", "zai"}, {"", ""}, {"unidentified", ""},
	} {
		source := usageSource(domain.UsageSourceQwenMonthly)
		source.NativeRootID = "qwen-session"
		source.ProviderHint = tc.hint
		result := parseRecords(source, []jsonlRecord{record}, 100, time.Now())
		if result.err != nil || len(result.Events) != 1 {
			t.Fatalf("hint %q: result = %+v", tc.hint, result)
		}
		event := result.Events[0]
		if providerUsageTokens(t, event.ProviderUsageJSON, derivedCacheWriteKey) != 0 {
			t.Fatalf("hint %q: provider usage = %s, want a derived zero cache write", tc.hint, event.ProviderUsageJSON)
		}
		wantSource := domain.ObservedBillingProviderSource(tc.want)
		if event.BillingProviderID != tc.want || event.BillingProviderSource != wantSource {
			t.Fatalf("hint %q: billing = %q/%q, want %q/%q",
				tc.hint, event.BillingProviderID, event.BillingProviderSource, tc.want, wantSource)
		}
	}
}
