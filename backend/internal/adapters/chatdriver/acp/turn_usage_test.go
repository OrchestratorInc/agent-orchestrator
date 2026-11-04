package acp

import (
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func intPointer(value int) *int { return &value }

func TestACPTurnUsageKeepsCacheReadsAndWritesApart(t *testing.T) {
	// Cache and thought counts outside input and output, as opencode and the
	// Claude adapter report them.
	assertACPTurnUsage(t, acpsdk.Usage{
		InputTokens: 100, OutputTokens: 20, CachedReadTokens: intPointer(300),
		CachedWriteTokens: intPointer(50), ThoughtTokens: intPointer(5), TotalTokens: 475,
	}, 300, 150, int64Pointer(50), 25, int64Pointer(5))
	// Input already counts the cache, output already counts thinking.
	assertACPTurnUsage(t, acpsdk.Usage{
		InputTokens: 450, OutputTokens: 25, CachedReadTokens: intPointer(300),
		CachedWriteTokens: intPointer(50), ThoughtTokens: intPointer(5), TotalTokens: 475,
	}, 300, 150, int64Pointer(50), 25, int64Pointer(5))
	// No total to tell by: cache counts are taken as separate.
	assertACPTurnUsage(t, acpsdk.Usage{
		InputTokens: 100, OutputTokens: 20, CachedReadTokens: intPointer(300),
	}, 300, 100, nil, 20, nil)
}

func assertACPTurnUsage(
	t *testing.T,
	usage acpsdk.Usage,
	wantCached, wantUncached int64,
	wantWrite *int64,
	wantOutput int64,
	wantReasoning *int64,
) {
	t.Helper()
	got := acpTurnUsage(usage, " model-a ", nil)
	if got.ModelID != "model-a" || got.CachedInputTokens != wantCached ||
		got.UncachedInputTokens != wantUncached || got.OutputTokens != wantOutput {
		t.Fatalf("turn usage of %+v = %+v, want cached %d uncached %d output %d",
			usage, got, wantCached, wantUncached, wantOutput)
	}
	if !sameTokens(got.CacheWriteTokens, wantWrite) {
		t.Fatalf("cache writes of %+v = %v, want %v", usage, got.CacheWriteTokens, wantWrite)
	}
	if !sameTokens(got.ReasoningTokens, wantReasoning) {
		t.Fatalf("reasoning of %+v = %v, want %v", usage, got.ReasoningTokens, wantReasoning)
	}
	if got.Cost != nil {
		t.Fatalf("cost = %v, want none without a reading", *got.Cost)
	}
}

func sameTokens(got, want *int64) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}

func TestACPTurnCostIsTheCumulativeDifference(t *testing.T) {
	start := &acpCost{amount: 1.5, currency: "USD"}
	end := &acpCost{amount: 1.75, currency: "USD"}
	turn := acpTurnUsage(acpsdk.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, "", costSince(start, end))
	if turn.Cost == nil || *turn.Cost != 0.25 || turn.Currency != "USD" {
		t.Fatalf("turn cost = %v %q, want 0.25 USD", turn.Cost, turn.Currency)
	}
	if costSince(nil, end) != nil {
		t.Fatal("a turn with no starting reading was charged the whole session's cost")
	}
	if costSince(start, &acpCost{amount: 2, currency: "EUR"}) != nil {
		t.Fatal("readings in different currencies were subtracted")
	}
}

func int64Pointer(value int64) *int64 { return &value }
