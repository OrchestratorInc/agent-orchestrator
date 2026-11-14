package usage

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

func gooseFixtureDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions", "sessions.db")
	mustNoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	writer, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	mustNoError(t, err)
	writer.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = writer.Close() })
	_, err = writer.Exec(`
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    working_dir TEXT NOT NULL DEFAULT '',
    provider_name TEXT,
    parent_session_id TEXT
);
CREATE TABLE usage_ledger (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    created_timestamp INTEGER NOT NULL,
    model TEXT,
    input_tokens INTEGER,
    output_tokens INTEGER,
    total_tokens INTEGER,
    cache_read_tokens INTEGER,
    cache_write_tokens INTEGER,
    cost REAL,
    cost_source TEXT,
    is_compaction INTEGER DEFAULT 0
);
INSERT INTO sessions (id, provider_name, parent_session_id) VALUES
    ('20261103_1', 'anthropic', NULL),
    ('20261103_2', 'openai', '20261103_1'),
    ('20261103_3', 'anthropic', NULL);`)
	mustNoError(t, err)
	return path, writer
}

func insertGooseLedgerRow(t *testing.T, writer *sql.DB, sessionID, model string, input, output, cacheRead, cacheWrite int64, cost any, costSource string) {
	t.Helper()
	_, err := writer.Exec(`INSERT INTO usage_ledger (
    session_id, created_timestamp, model, input_tokens, output_tokens,
    total_tokens, cache_read_tokens, cache_write_tokens, cost, cost_source
) VALUES (?, 1793750400, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, model, input, output, input+output, cacheRead, cacheWrite, cost, costSource)
	mustNoError(t, err)
}

func TestReadGooseLedgerRowsForSessionTree(t *testing.T) {
	path, writer := gooseFixtureDB(t)
	insertGooseLedgerRow(t, writer, "20261103_1", "claude-sonnet-4-5", 100, 20, 30, 10, 0.0125, "provider_reported")
	insertGooseLedgerRow(t, writer, "20261103_3", "claude-sonnet-4-5", 999, 999, 0, 0, nil, "")
	insertGooseLedgerRow(t, writer, "20261103_2", "gpt-5", 50, 5, 20, 0, 0.5, "estimated")
	_, err := writer.Exec(`INSERT INTO usage_ledger (session_id, created_timestamp, input_tokens, output_tokens, cost_source)
VALUES ('20261103_1', 1793750400, 7, 7, 'carried_forward')`)
	mustNoError(t, err)
	before, err := os.ReadFile(path)
	mustNoError(t, err)

	ctx := context.Background()
	db, err := openSQLiteUsageDB(ctx, path)
	mustNoError(t, err)
	defer func() { _ = db.Close() }()
	records, err := readGooseLedgerRows(ctx, db, "20261103_1", 0, 10)
	mustNoError(t, err)
	if len(records) != 3 || records[0].Offset != 1 || records[1].Offset != 3 || records[2].Offset != 4 {
		t.Fatalf("records = %+v", records)
	}
	if _, err := db.Exec(`DELETE FROM usage_ledger`); err == nil {
		t.Fatal("usage database connection accepted a write")
	}

	source := usageSource(domain.UsageSourceGooseLedger)
	source.NativeRootID = "20261103_1"
	result := parseRecords(source, records, records[len(records)-1].Offset, time.Unix(1700000000, 0).UTC())
	if result.err != nil || len(result.Events) != 2 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	root := result.Events[0]
	if root.ProviderID != domain.UsageProviderAnthropic || root.BillingProviderID != "anthropic" ||
		root.BillingProviderSource != domain.UsageBillingProviderObserved ||
		root.ModelID != "claude-sonnet-4-5" || root.MeasurementKind != domain.UsageMeasurementNativeReported ||
		!root.CreatedAt.Equal(time.Unix(1793750400, 0).UTC()) {
		t.Fatalf("root event = %+v", root)
	}
	if tokenValue(root.Tokens.InputTokens) != 100 || tokenValue(root.Tokens.CachedInputTokens) != 30 ||
		tokenValue(root.Tokens.UncachedInputTokens) != 70 || tokenValue(root.Tokens.OutputTokens) != 20 {
		t.Fatalf("root tokens = %+v", root.Tokens)
	}
	if root.Costs.EstimatedCostNanos == nil || *root.Costs.EstimatedCostNanos != 12_500_000 ||
		root.Costs.PricingVersion != goosePricingVersion {
		t.Fatalf("root costs = %+v, want Goose's provider-reported cost", root.Costs)
	}
	if providerUsageTokens(t, root.ProviderUsageJSON, derivedCacheWriteKey) != 10 {
		t.Fatalf("provider usage = %s", root.ProviderUsageJSON)
	}
	child := result.Events[1]
	if child.ProviderID != domain.UsageProviderOpenAI || child.BillingProviderID != "openai" ||
		tokenValue(child.Tokens.CachedInputTokens) != 20 || tokenValue(child.Tokens.UncachedInputTokens) != 30 {
		t.Fatalf("child event = %+v", child)
	}
	if child.Costs.EstimatedCostNanos != nil {
		t.Fatalf("child costs = %+v, want catalog pricing for Goose's own estimate", child.Costs)
	}

	newer, err := readGooseLedgerRows(ctx, db, "20261103_1", 4, 10)
	mustNoError(t, err)
	if len(newer) != 0 {
		t.Fatalf("rows after cursor = %+v", newer)
	}
	after, err := os.ReadFile(path)
	mustNoError(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("reading the usage database modified it")
	}
}

func TestParseGooseRejectsCacheExceedingInput(t *testing.T) {
	source := usageSource(domain.UsageSourceGooseLedger)
	source.NativeRootID = "20261103_1"
	record := jsonlRecord{Offset: 1, Data: []byte(`{"id":1,"session_id":"20261103_1","model":"claude","provider_name":"anthropic","input_tokens":5,"output_tokens":1,"cache_read_tokens":4,"cache_write_tokens":2}`)}
	result := parseRecords(source, []jsonlRecord{record}, 1, time.Now())
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(result.Events) != 0 || result.Cursor.AnomalyCount != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestIngestorReadsGooseLedgerIncrementally(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	path, writer := gooseFixtureDB(t)
	insertGooseLedgerRow(t, writer, "20261103_1", "claude-sonnet-4-5", 100, 20, 30, 10, 0.0125, "provider_reported")
	store, session := seedUsageTestSession(
		t, t.TempDir(), "usage", domain.HarnessGoose, domain.ActivityActive, "20261103_1", now,
	)
	binding := seedUsageTestBinding(t, store, session, "20261103_1", domain.UsageBindingActive, now)
	identity, err := usagesvc.SourceIdentity(ctx, path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceGooseLedger,
		NativeSessionID: "20261103_1",
		ArtifactPath:    path,
		FileIdentity:    identity,
		State:           domain.UsageSourcePending,
		UpdatedAt:       now,
	})
	mustNoError(t, err)
	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})

	result, err := ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	if result.More || result.RetryAt != nil {
		t.Fatalf("first result = %+v, want one complete read and no poll", result)
	}
	assertTokenAggregate(t, store, session.ID, 120)

	insertGooseLedgerRow(t, writer, "20261103_1", "claude-sonnet-4-5", 40, 4, 0, 0, nil, "")
	_, err = ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	assertTokenAggregate(t, store, session.ID, 164)
	persisted, ok, err := store.GetUsageSourceForIngestion(ctx, source.ID)
	mustNoError(t, err)
	if !ok || persisted.Source.ByteOffset != 2 || persisted.Source.State != domain.UsageSourceActive {
		t.Fatalf("source cursor = %+v", persisted.Source)
	}
	now = now.Add(time.Minute)
	_, err = ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	assertTokenAggregate(t, store, session.ID, 164)
	polled, ok, err := store.GetUsageSourceForIngestion(ctx, source.ID)
	mustNoError(t, err)
	if !ok || !polled.Source.UpdatedAt.Equal(persisted.Source.UpdatedAt) {
		t.Fatalf("empty poll rewrote the cursor: %+v after %+v", polled.Source, persisted.Source)
	}
}
