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

const fxTestSessionID = "hPP5JgsicDzo"

// writeFXUsage rewrites a session's usage-v2.json in place, as fx does, with
// the given models' running totals.
func writeFXUsage(t *testing.T, path, sessionID string, models ...string) {
	t.Helper()
	mustNoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	content := fmt.Sprintf(`{"schema_version":1,"session_id":%q,"snapshot":{"billing":"complete","models":[`, sessionID)
	for index, model := range models {
		if index > 0 {
			content += ","
		}
		content += model
	}
	content += `]}}`
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	mustNoError(t, err)
	_, err = file.WriteString(content)
	mustNoError(t, err)
	mustNoError(t, file.Close())
}

func fxModel(model string, input, cacheRead, cacheWrite, output int64, cost float64) string {
	return fmt.Sprintf(`{"model":%q,"first_sequence":1,"total_cost":%g,"input_tokens":%d,"output_tokens":%d,`+
		`"cache_read_tokens":%d,"cache_write_tokens":%d,"reasoning_tokens":0,"request_count":1,"billable_web_search_calls":0}`,
		model, cost, input, output, cacheRead, cacheWrite)
}

func fxTestUsagePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "sessions", fxTestSessionID, "usage-v2.json")
}

func readFXFixture(t *testing.T, path string) sqliteUsageChunk {
	t.Helper()
	chunk, err := readFXUsageChunk(context.Background(), path, domain.UsageSourceRecord{NativeSessionID: fxTestSessionID})
	mustNoError(t, err)
	return chunk
}

// TestParseFXEmitsOnlyTotalGrowth catches billing fx's running totals again on
// every read, mislabeling a Codex or gateway model, inventing a cost for a
// subscription connection, and billing negative usage when totals shrink.
func TestParseFXEmitsOnlyTotalGrowth(t *testing.T) {
	path := fxTestUsagePath(t)
	writeFXUsage(t, path, fxTestSessionID,
		fxModel("codex/gpt-5.5", 18353, 8704, 0, 82, 0),
		fxModel("gateway/anthropic/claude-sonnet-4-5", 1000, 600, 100, 40, 0.0125),
	)
	source := usageSource(domain.UsageSourceFXSession)
	source.NativeRootID = fxTestSessionID
	state, err := newParserState(domain.UsageSourceFXSession)
	mustNoError(t, err)
	now := time.Unix(1700000000, 0).UTC()

	chunk := readFXFixture(t, path)
	first := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if first.err != nil || len(first.Events) != 2 || first.Cursor.AnomalyCount != 0 {
		t.Fatalf("first pass = %+v", first)
	}
	codex, claude := first.Events[0], first.Events[1]
	if codex.ProviderID != domain.UsageProviderOpenAI || codex.BillingProviderID != "openai" || codex.ModelID != "gpt-5.5" ||
		tokenValue(codex.Tokens.InputTokens) != 18353 || tokenValue(codex.Tokens.CachedInputTokens) != 8704 ||
		tokenValue(codex.Tokens.OutputTokens) != 82 || codex.Costs.EstimatedCostNanos != nil {
		t.Fatalf("codex event = %+v", codex)
	}
	if claude.ProviderID != domain.UsageProviderAnthropic || claude.BillingProviderID != "anthropic" ||
		claude.ModelID != "claude-sonnet-4-5" || tokenValue(claude.Tokens.InputTokens) != 1000 ||
		tokenValue(claude.Tokens.CachedInputTokens) != 600 || tokenValue(claude.Tokens.UncachedInputTokens) != 400 {
		t.Fatalf("gateway event = %+v", claude)
	}
	if claude.Costs.EstimatedCostNanos == nil || *claude.Costs.EstimatedCostNanos != 12_500_000 ||
		claude.Costs.PricingVersion != fxPricingVersion {
		t.Fatalf("gateway costs = %+v, want fx's reported cost", claude.Costs)
	}

	replay := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if replay.err != nil || len(replay.Events) != 0 {
		t.Fatalf("replay of the same totals = %+v", replay)
	}

	writeFXUsage(t, path, fxTestSessionID,
		fxModel("codex/gpt-5.5", 28353, 16704, 0, 100, 0),
		fxModel("gateway/anthropic/claude-sonnet-4-5", 1000, 600, 100, 40, 0.0125),
	)
	chunk = readFXFixture(t, path)
	second := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if second.err != nil || len(second.Events) != 1 {
		t.Fatalf("second pass = %+v", second)
	}
	if growth := second.Events[0]; tokenValue(growth.Tokens.InputTokens) != 10000 ||
		tokenValue(growth.Tokens.CachedInputTokens) != 8000 || tokenValue(growth.Tokens.OutputTokens) != 18 ||
		growth.SourceEventKey == codex.SourceEventKey {
		t.Fatalf("growth event = %+v", growth)
	}

	writeFXUsage(t, path, fxTestSessionID, fxModel("codex/gpt-5.5", 10, 0, 0, 1, 0))
	chunk = readFXFixture(t, path)
	shrunk := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if shrunk.err != nil || len(shrunk.Events) != 0 || shrunk.Cursor.AnomalyCount != 1 ||
		shrunk.Cursor.LastErrorCode != domain.UsageErrorNonMonotonicCumulativeUsage {
		t.Fatalf("shrunk totals = %+v, want an anomaly and no negative usage", shrunk)
	}
}

func TestReadFXRejectsAnotherSession(t *testing.T) {
	path := fxTestUsagePath(t)
	writeFXUsage(t, path, "K8N2QX5pmQBw", fxModel("codex/gpt-5.5", 10, 0, 0, 1, 0))
	if _, err := readFXUsageChunk(context.Background(), path, domain.UsageSourceRecord{NativeSessionID: fxTestSessionID}); err == nil {
		t.Fatal("read another session's usage-v2.json")
	}
}

// TestIngestorKeepsFXBaselineAcrossRewrites catches the ingestor losing the
// baseline between passes over a file fx rewrites in place.
func TestIngestorKeepsFXBaselineAcrossRewrites(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	path := fxTestUsagePath(t)
	writeFXUsage(t, path, fxTestSessionID, fxModel("codex/gpt-5.5", 100, 40, 0, 20, 0))
	store, session := seedUsageTestSession(
		t, t.TempDir(), "usage", domain.HarnessFX, domain.ActivityActive, fxTestSessionID, now,
	)
	binding := seedUsageTestBinding(t, store, session, fxTestSessionID, domain.UsageBindingActive, now)
	identity, err := usagesvc.SourceIdentity(ctx, path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceFXSession,
		NativeSessionID: fxTestSessionID,
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
	writeFXUsage(t, path, fxTestSessionID, fxModel("codex/gpt-5.5", 150, 40, 0, 30, 0))
	_, err = ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	assertTokenAggregate(t, store, session.ID, 180)
}
