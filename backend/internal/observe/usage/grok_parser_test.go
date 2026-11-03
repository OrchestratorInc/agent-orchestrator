package usage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

const (
	grokTestSessionID = "01a149b7-193c-7ae1-9e08-661fcfb1e60d"
	grokTestParentID  = "01a149b7-0000-7ae1-9e08-661fcfb1e60d"
)

// writeGrokUsage rewrites a session's usage.json in place, as Grok does, with
// one model's running totals.
func writeGrokUsage(t *testing.T, path, sessionID string, input, cached, output, calls, ticks int64) {
	t.Helper()
	mustNoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	content := fmt.Sprintf(`{"sessionId":%q,"updatedAt":"2026-10-17T12:36:17.595824+00:00","session":{
"inputTokens":%[2]d,"outputTokens":%[4]d,"cachedReadTokens":%[3]d,"cacheCreationTokens":0,"reasoningTokens":1,
"costUsdTicks":%[6]d,"modelCalls":%[5]d,"primaryModelId":"grok-4.7","modelUsage":{"grok-4.7":{
"inputTokens":%[2]d,"outputTokens":%[4]d,"cachedReadTokens":%[3]d,"cacheCreationTokens":0,"reasoningTokens":1,
"costUsdTicks":%[6]d,"modelCalls":%[5]d}}},"turns":[]}`, sessionID, input, cached, output, calls, ticks)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	mustNoError(t, err)
	_, err = file.WriteString(content)
	mustNoError(t, err)
	mustNoError(t, file.Close())
}

func grokTestUsagePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "sessions", "%2Fwork%2Frepo", grokTestSessionID, "usage.json")
}

func readGrokFixture(t *testing.T, path string) sqliteUsageChunk {
	t.Helper()
	chunk, err := readGrokUsageChunk(context.Background(), path, domain.UsageSourceRecord{NativeSessionID: grokTestSessionID})
	mustNoError(t, err)
	return chunk
}

// TestParseGrokEmitsOnlyTotalGrowth catches billing Grok's running totals
// again on every read, mis-scaling costUsdTicks, and billing negative usage
// when the totals shrink.
func TestParseGrokEmitsOnlyTotalGrowth(t *testing.T) {
	path := grokTestUsagePath(t)
	// 1e10 ticks per USD: 811,560,000 ticks is $0.081156.
	writeGrokUsage(t, path, grokTestSessionID, 90720, 69632, 694, 4, 811_560_000)
	source := usageSource(domain.UsageSourceGrokSession)
	source.NativeRootID = grokTestSessionID
	state, err := newParserState(domain.UsageSourceGrokSession)
	mustNoError(t, err)
	now := time.Unix(1700000000, 0).UTC()

	chunk := readGrokFixture(t, path)
	first := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if first.err != nil || len(first.Events) != 1 || first.Cursor.AnomalyCount != 0 {
		t.Fatalf("first pass = %+v", first)
	}
	event := first.Events[0]
	if event.ProviderID != domain.UsageProviderOpenAI || event.BillingProviderID != "xai" || event.ModelID != "grok-4.7" ||
		tokenValue(event.Tokens.InputTokens) != 90720 || tokenValue(event.Tokens.CachedInputTokens) != 69632 ||
		tokenValue(event.Tokens.UncachedInputTokens) != 21088 || tokenValue(event.Tokens.OutputTokens) != 694 {
		t.Fatalf("event = %+v", event)
	}
	if event.Costs.EstimatedCostNanos == nil || *event.Costs.EstimatedCostNanos != 81_156_000 ||
		event.Costs.PricingVersion != grokPricingVersion {
		t.Fatalf("costs = %+v, want Grok's reported cost in nanodollars", event.Costs)
	}

	replay := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if replay.err != nil || len(replay.Events) != 0 {
		t.Fatalf("replay of the same totals = %+v", replay)
	}

	writeGrokUsage(t, path, grokTestSessionID, 100720, 79632, 794, 5, 900_000_000)
	chunk = readGrokFixture(t, path)
	second := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if second.err != nil || len(second.Events) != 1 {
		t.Fatalf("second pass = %+v", second)
	}
	growth := second.Events[0]
	if tokenValue(growth.Tokens.InputTokens) != 10000 || tokenValue(growth.Tokens.OutputTokens) != 100 ||
		*growth.Costs.EstimatedCostNanos != 8_844_000 || growth.SourceEventKey == event.SourceEventKey {
		t.Fatalf("growth event = %+v", growth)
	}

	writeGrokUsage(t, path, grokTestSessionID, 10, 0, 1, 1, 0)
	chunk = readGrokFixture(t, path)
	shrunk := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if shrunk.err != nil || len(shrunk.Events) != 0 || shrunk.Cursor.AnomalyCount != 1 ||
		shrunk.Cursor.LastErrorCode != domain.UsageErrorNonMonotonicCumulativeUsage {
		t.Fatalf("shrunk totals = %+v, want an anomaly and no negative usage", shrunk)
	}
}

// TestParseGrokSkipsInheritedForkHistory catches billing a forked session's
// copy of its parent's history, which the parent already reported.
func TestParseGrokSkipsInheritedForkHistory(t *testing.T) {
	path := grokTestUsagePath(t)
	workspace := filepath.Dir(filepath.Dir(path))
	writeGrokUsage(t, filepath.Join(workspace, grokTestParentID, "usage.json"), grokTestParentID, 1000, 0, 100, 2, 0)
	writeGrokUsage(t, path, grokTestSessionID, 1500, 0, 130, 3, 0)
	mustNoError(t, os.WriteFile(
		filepath.Join(filepath.Dir(path), "summary.json"),
		[]byte(`{"parent_session_id":"`+grokTestParentID+`"}`),
		0o600,
	))
	source := usageSource(domain.UsageSourceGrokSession)
	source.NativeRootID = grokTestSessionID
	state, err := newParserState(domain.UsageSourceGrokSession)
	mustNoError(t, err)

	chunk := readGrokFixture(t, path)
	parsed := parseRecordsWithState(source, chunk.records, chunk.nextOffset, time.Now(), state)
	if parsed.err != nil || len(parsed.Events) != 1 {
		t.Fatalf("parsed = %+v", parsed)
	}
	if got := parsed.Events[0]; tokenValue(got.Tokens.InputTokens) != 500 || tokenValue(got.Tokens.OutputTokens) != 30 {
		t.Fatalf("fork event = %+v, want only the fork's own usage", got)
	}
}

func TestReadGrokRejectsAnotherSession(t *testing.T) {
	path := grokTestUsagePath(t)
	writeGrokUsage(t, path, grokTestParentID, 10, 0, 1, 1, 0)
	if _, err := readGrokUsageChunk(context.Background(), path, domain.UsageSourceRecord{NativeSessionID: grokTestSessionID}); err == nil {
		t.Fatal("read another session's usage.json")
	}
}

// TestIngestorKeepsGrokBaselineAcrossRewrites catches the ingestor losing the
// baseline between passes over a file Grok rewrites in place.
func TestIngestorKeepsGrokBaselineAcrossRewrites(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	path := grokTestUsagePath(t)
	writeGrokUsage(t, path, grokTestSessionID, 100, 40, 20, 1, 0)
	store, session := seedUsageTestSession(
		t, t.TempDir(), "usage", domain.HarnessGrok, domain.ActivityActive, grokTestSessionID, now,
	)
	binding := seedUsageTestBinding(t, store, session, grokTestSessionID, domain.UsageBindingActive, now)
	identity, err := usagesvc.SourceIdentity(ctx, path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceGrokSession,
		NativeSessionID: grokTestSessionID,
		ArtifactPath:    path,
		FileIdentity:    identity,
		State:           domain.UsageSourcePending,
		UpdatedAt:       now,
	})
	mustNoError(t, err)
	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})

	for range 2 {
		_, err := ingestor.Ingest(ctx, source.ID)
		mustNoError(t, err)
	}
	assertTokenAggregate(t, store, session.ID, 120)
	writeGrokUsage(t, path, grokTestSessionID, 150, 40, 30, 2, 0)
	_, err = ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	assertTokenAggregate(t, store, session.ID, 180)
}
