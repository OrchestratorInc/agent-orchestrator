package usage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const grokTestSessionID = "01a1a16e-c4c7-7c51-92d5-bf8beef81073"

const grokUsageSnapshot = `{"sessionId":"` + grokTestSessionID + `","updatedAt":"2026-11-03T12:44:10.000000+00:00",
"session":{"inputTokens":61997,"outputTokens":283,"cachedReadTokens":42752,"cacheCreationTokens":0,"reasoningTokens":256,"totalTokens":62280,"modelCalls":3,"costUsdTicks":735380000,"turnCount":2,"primaryModelId":"grok-4.7"},
"turns":[
 {"turnNumber":2,"endedAt":"2026-11-03T12:42:59.918628+00:00","inputTokens":20570,"outputTokens":47,"cachedReadTokens":1664,"cacheCreationTokens":0,"reasoningTokens":46,"totalTokens":20617,"modelCalls":1,"costUsdTicks":389260000,"turnCount":1,"primaryModelId":"grok-4.7",
  "modelUsage":{"grok-4.7":{"inputTokens":20570,"outputTokens":47,"cachedReadTokens":1664,"cacheCreationTokens":0,"reasoningTokens":46,"totalTokens":20617,"modelCalls":1,"costUsdTicks":389260000}}},
 {"turnNumber":3,"endedAt":"2026-11-03T12:44:10.000000+00:00","inputTokens":41427,"outputTokens":236,"cachedReadTokens":41088,"cacheCreationTokens":0,"reasoningTokens":130,"totalTokens":41663,"modelCalls":2,"turnCount":1,"primaryModelId":"grok-4.7"}
]}`

func TestReadAndParseGrokUsageSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "%2Frepo", grokTestSessionID, "usage.json")
	mustNoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	mustNoError(t, os.WriteFile(path, []byte(grokUsageSnapshot), 0o600))

	ctx := context.Background()
	records, err := readGrokTurns(ctx, path, grokTestSessionID, 0, 10)
	mustNoError(t, err)
	if len(records) != 2 || records[0].Offset != 2<<grokCursorModelBits || records[1].Offset != 3<<grokCursorModelBits {
		t.Fatalf("records = %+v", records)
	}
	newer, err := readGrokTurns(ctx, path, grokTestSessionID, records[0].Offset, 10)
	mustNoError(t, err)
	if len(newer) != 1 || newer[0].Offset != records[1].Offset {
		t.Fatalf("records after turn 2 = %+v", newer)
	}
	if _, err := readGrokTurns(ctx, path, "another-session", 0, 10); err == nil {
		t.Fatal("a usage file naming another session was read")
	}

	source := usageSource(domain.UsageSourceGrokUsage)
	source.NativeRootID = grokTestSessionID
	result := parseRecords(source, records, records[1].Offset, time.Now())
	if result.err != nil || len(result.Events) != 2 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	priced, unpriced := result.Events[0], result.Events[1]
	if priced.ProviderID != domain.UsageProviderOpenAI || priced.BillingProviderID != "xai" || priced.ModelID != "grok-4.7" ||
		tokenValue(priced.Tokens.InputTokens) != 20570 || tokenValue(priced.Tokens.CachedInputTokens) != 1664 ||
		tokenValue(priced.Tokens.UncachedInputTokens) != 18906 || tokenValue(priced.Tokens.OutputTokens) != 47 ||
		!priced.CreatedAt.Equal(time.Date(2026, 11, 3, 12, 42, 59, 918628000, time.UTC)) {
		t.Fatalf("priced event = %+v", priced)
	}
	if priced.Costs.EstimatedCostNanos == nil || *priced.Costs.EstimatedCostNanos != 38_926_000 ||
		priced.Costs.PricingVersion != grokPricingVersion {
		t.Fatalf("priced costs = %+v, want Grok's reported cost", priced.Costs)
	}
	if tokenValue(unpriced.Tokens.InputTokens) != 41427 || tokenValue(unpriced.Tokens.OutputTokens) != 236 ||
		unpriced.Costs.EstimatedCostNanos != nil {
		t.Fatalf("turn without a breakdown or cost = %+v", unpriced)
	}
	if priced.SourceEventKey == unpriced.SourceEventKey {
		t.Fatal("two turns share an event key")
	}
}
