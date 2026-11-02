package usage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

// hermesFixtureDB creates a minimal Hermes state.db in WAL mode with a root
// session, its compression child and an unrelated session.
func hermesFixtureDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hermes-home", "state.db")
	mustNoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	writer, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	mustNoError(t, err)
	writer.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = writer.Close() })
	_, err = writer.Exec(`
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT 'cli',
    parent_session_id TEXT,
    started_at REAL NOT NULL DEFAULT 0
);
CREATE TABLE session_model_usage (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    model TEXT NOT NULL,
    billing_provider TEXT NOT NULL DEFAULT '',
    billing_base_url TEXT NOT NULL DEFAULT '',
    billing_mode TEXT NOT NULL DEFAULT '',
    task TEXT NOT NULL DEFAULT '',
    api_call_count INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    estimated_cost_usd REAL NOT NULL DEFAULT 0,
    actual_cost_usd REAL NOT NULL DEFAULT 0,
    cost_status TEXT,
    cost_source TEXT,
    first_seen REAL,
    last_seen REAL,
    PRIMARY KEY (session_id, model, billing_provider, billing_base_url, billing_mode, task)
);
INSERT INTO sessions (id, parent_session_id) VALUES
    ('20261103_100000_aaaa', NULL),
    ('20261103_110000_bbbb', '20261103_100000_aaaa'),
    ('20261103_120000_cccc', NULL);`)
	mustNoError(t, err)
	return path, writer
}

// addHermesUsage mirrors Hermes's per-call upsert, which only ever adds.
func addHermesUsage(t *testing.T, writer *sql.DB, sessionID, model, provider string, input, output, cacheRead, cacheWrite int64, cost float64, status string) {
	t.Helper()
	_, err := writer.Exec(`INSERT INTO session_model_usage (
    session_id, model, billing_provider, api_call_count, input_tokens, output_tokens,
    cache_read_tokens, cache_write_tokens, estimated_cost_usd, cost_status, first_seen, last_seen
) VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?, 1793750400, 1793750400)
ON CONFLICT(session_id, model, billing_provider, billing_base_url, billing_mode, task) DO UPDATE SET
    api_call_count = api_call_count + 1,
    input_tokens = input_tokens + excluded.input_tokens,
    output_tokens = output_tokens + excluded.output_tokens,
    cache_read_tokens = cache_read_tokens + excluded.cache_read_tokens,
    cache_write_tokens = cache_write_tokens + excluded.cache_write_tokens,
    estimated_cost_usd = estimated_cost_usd + excluded.estimated_cost_usd,
    cost_status = COALESCE(excluded.cost_status, cost_status),
    last_seen = excluded.last_seen`,
		sessionID, model, provider, input, output, cacheRead, cacheWrite, cost, status)
	mustNoError(t, err)
}

func readHermesFixture(t *testing.T, path string) sqliteUsageChunk {
	t.Helper()
	ctx := context.Background()
	db, err := openSQLiteUsageDB(ctx, path)
	mustNoError(t, err)
	defer func() { _ = db.Close() }()
	chunk, err := readHermesUsageChunk(ctx, db, domain.UsageSourceRecord{NativeSessionID: "20261103_100000_aaaa"}, 0)
	mustNoError(t, err)
	return chunk
}

// TestParseHermesEmitsOnlyCounterGrowth catches billing Hermes's cumulative
// counters again on every read, dropping a compression child's calls, and
// attributing another session's usage.
func TestParseHermesEmitsOnlyCounterGrowth(t *testing.T) {
	path, writer := hermesFixtureDB(t)
	addHermesUsage(t, writer, "20261103_100000_aaaa", "claude-sonnet-4-5", "anthropic", 70, 20, 30, 10, 0.0125, "actual")
	addHermesUsage(t, writer, "20261103_110000_bbbb", "gpt-5", "openrouter", 30, 5, 20, 0, 0.5, "estimated")
	addHermesUsage(t, writer, "20261103_120000_cccc", "gpt-5", "openai", 999, 999, 0, 0, 0, "")

	source := usageSource(domain.UsageSourceHermesState)
	source.NativeRootID = "20261103_100000_aaaa"
	state, err := newParserState(domain.UsageSourceHermesState)
	mustNoError(t, err)
	now := time.Unix(1700000000, 0).UTC()

	chunk := readHermesFixture(t, path)
	if len(chunk.records) != 2 || !chunk.atEnd || chunk.nextOffset <= 0 {
		t.Fatalf("chunk = %+v", chunk)
	}
	first := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if first.err != nil || len(first.Events) != 2 || first.Cursor.AnomalyCount != 0 {
		t.Fatalf("first pass = %+v", first)
	}
	root := first.Events[0]
	if root.ProviderID != domain.UsageProviderAnthropic || root.BillingProviderID != "anthropic" ||
		tokenValue(root.Tokens.InputTokens) != 110 || tokenValue(root.Tokens.CachedInputTokens) != 30 ||
		tokenValue(root.Tokens.UncachedInputTokens) != 80 || tokenValue(root.Tokens.OutputTokens) != 20 {
		t.Fatalf("root event = %+v", root)
	}
	if root.Costs.EstimatedCostNanos == nil || *root.Costs.EstimatedCostNanos != 12_500_000 ||
		root.Costs.PricingVersion != hermesPricingVersion {
		t.Fatalf("root costs = %+v, want Hermes's provider-returned cost", root.Costs)
	}
	child := first.Events[1]
	if child.ProviderID != domain.UsageProviderOpenAI || child.BillingProviderID != "openrouter" ||
		tokenValue(child.Tokens.InputTokens) != 50 || tokenValue(child.Tokens.CachedInputTokens) != 20 ||
		child.Costs.EstimatedCostNanos != nil {
		t.Fatalf("child event = %+v", child)
	}

	replay := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if replay.err != nil || len(replay.Events) != 0 {
		t.Fatalf("replay of the same counters = %+v", replay)
	}

	addHermesUsage(t, writer, "20261103_100000_aaaa", "claude-sonnet-4-5", "anthropic", 7, 2, 3, 0, 0, "")
	chunk = readHermesFixture(t, path)
	second := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if second.err != nil || len(second.Events) != 1 {
		t.Fatalf("second pass = %+v", second)
	}
	growth := second.Events[0]
	if tokenValue(growth.Tokens.InputTokens) != 10 || tokenValue(growth.Tokens.OutputTokens) != 2 ||
		growth.SourceEventKey == root.SourceEventKey {
		t.Fatalf("growth event = %+v", growth)
	}

	_, err = writer.Exec(`UPDATE session_model_usage SET input_tokens = 1 WHERE session_id = '20261103_110000_bbbb'`)
	mustNoError(t, err)
	chunk = readHermesFixture(t, path)
	shrunk := parseRecordsWithState(source, chunk.records, chunk.nextOffset, now, state)
	if shrunk.err != nil || len(shrunk.Events) != 0 || shrunk.Cursor.AnomalyCount != 1 ||
		shrunk.Cursor.LastErrorCode != domain.UsageErrorNonMonotonicCumulativeUsage {
		t.Fatalf("shrunk counters = %+v, want an anomaly and no negative usage", shrunk)
	}
}

// TestIngestorKeepsHermesBaselineAcrossPasses catches the ingestor losing the
// counter baseline between passes, which would bill every call repeatedly.
func TestIngestorKeepsHermesBaselineAcrossPasses(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	path, writer := hermesFixtureDB(t)
	addHermesUsage(t, writer, "20261103_100000_aaaa", "claude-sonnet-4-5", "anthropic", 70, 20, 30, 10, 0, "")
	store, session := seedUsageTestSession(
		t, t.TempDir(), "usage", domain.HarnessHermes, domain.ActivityActive, "20261103_100000_aaaa", now,
	)
	binding := seedUsageTestBinding(t, store, session, "20261103_100000_aaaa", domain.UsageBindingActive, now)
	identity, err := usagesvc.SourceIdentity(ctx, path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceHermesState,
		NativeSessionID: "20261103_100000_aaaa",
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
	assertTokenAggregate(t, store, session.ID, 130)
	addHermesUsage(t, writer, "20261103_100000_aaaa", "claude-sonnet-4-5", "anthropic", 7, 3, 0, 0, 0, "")
	_, err = ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	assertTokenAggregate(t, store, session.ID, 140)
}
