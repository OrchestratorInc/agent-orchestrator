package usage

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

const (
	openCodeTestRoot  = "ses_root"
	openCodeTestChild = "ses_child"
	openCodeTestOther = "ses_other"
	openCodeTestTime  = int64(1793465755757)
)

func openCodeFixtureDB(t *testing.T, name string) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", name)
	mustNoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	writer, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	mustNoError(t, err)
	writer.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = writer.Close() })
	_, err = writer.Exec(`
CREATE TABLE session (
    id text PRIMARY KEY,
    project_id text NOT NULL DEFAULT 'global',
    parent_id text,
    slug text NOT NULL DEFAULT '',
    directory text NOT NULL DEFAULT '/work',
    title text NOT NULL DEFAULT '',
    version text NOT NULL DEFAULT '1.19.3',
    cost real DEFAULT 0 NOT NULL,
    tokens_input integer DEFAULT 0 NOT NULL,
    tokens_output integer DEFAULT 0 NOT NULL,
    time_created integer NOT NULL DEFAULT 0,
    time_updated integer NOT NULL DEFAULT 0,
    time_archived integer
);
CREATE TABLE message (
    id text PRIMARY KEY,
    session_id text NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    time_created integer NOT NULL,
    time_updated integer NOT NULL,
    data text NOT NULL
);
CREATE TABLE part (
    id text PRIMARY KEY,
    message_id text NOT NULL REFERENCES message(id) ON DELETE CASCADE,
    session_id text NOT NULL,
    time_created integer NOT NULL,
    time_updated integer NOT NULL,
    data text NOT NULL
);
CREATE INDEX part_session_idx ON part (session_id);
INSERT INTO session (id, parent_id) VALUES
    ('ses_root', NULL),
    ('ses_child', 'ses_root'),
    ('ses_other', NULL);`)
	mustNoError(t, err)
	return path, writer
}

func insertOpenCodeStep(
	t *testing.T, writer *sql.DB, sessionID, id, provider, model string, at int64,
	input, output, reasoning, cacheRead, cacheWrite int64, cost float64,
) {
	t.Helper()
	message := fmt.Sprintf(`{"role":"assistant","providerID":%q,"modelID":%q,"cost":%v,"tokens":{"input":%d,"output":%d,"reasoning":%d,"cache":{"read":%d,"write":%d}}}`,
		provider, model, cost, input, output, reasoning, cacheRead, cacheWrite)
	_, err := writer.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"msg_"+id, sessionID, at-100, at, message)
	mustNoError(t, err)
	step := fmt.Sprintf(`{"reason":"stop","type":"step-finish","tokens":{"total":%d,"input":%d,"output":%d,"reasoning":%d,"cache":{"write":%d,"read":%d}},"cost":%v}`,
		input+output+reasoning+cacheRead+cacheWrite, input, output, reasoning, cacheWrite, cacheRead, cost)
	_, err = writer.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
		"prt_"+id, "msg_"+id, sessionID, at, at, step)
	mustNoError(t, err)
}

func openCodeTestSource(kind domain.UsageSourceKind) domain.UsageSourceContext {
	source := usageSource(kind)
	source.NativeRootID = openCodeTestRoot
	return source
}

func TestReadOpenCodeStepsForSessionTree(t *testing.T) {
	path, writer := openCodeFixtureDB(t, "opencode.db")
	insertOpenCodeStep(t, writer, openCodeTestRoot, "a", "anthropic", "claude-sonnet-4-5", openCodeTestTime, 70, 15, 5, 20, 10, 0.0125)
	insertOpenCodeStep(t, writer, openCodeTestOther, "b", "zai", "glm-4.5", openCodeTestTime+1, 999, 999, 0, 0, 0, 0)
	insertOpenCodeStep(t, writer, openCodeTestChild, "c", "zai-coding-plan", "glm-5.2", openCodeTestTime+2, 176, 51, 22, 21824, 0, 0)
	_, err := writer.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES
    ('prt_text', 'msg_a', 'ses_root', ?, ?, '{"type":"text","text":"hi"}'),
    ('prt_bad', 'msg_a', 'ses_root', ?, ?, 'not json')`,
		openCodeTestTime+3, openCodeTestTime+3, openCodeTestTime+4, openCodeTestTime+4)
	mustNoError(t, err)
	before, err := os.ReadFile(path)
	mustNoError(t, err)

	ctx := context.Background()
	db, err := openSQLiteUsageDB(ctx, path)
	mustNoError(t, err)
	defer func() { _ = db.Close() }()
	records, err := readOpenCodeSteps(ctx, db, openCodeTestRoot, 0, 10)
	mustNoError(t, err)
	if len(records) != 2 || records[0].Offset >= records[1].Offset ||
		records[0].Offset>>openCodeCursorTieBits != openCodeTestTime ||
		records[1].Offset>>openCodeCursorTieBits != openCodeTestTime+2 {
		t.Fatalf("records = %+v", records)
	}
	child, err := readOpenCodeSteps(ctx, db, openCodeTestChild, 0, 10)
	mustNoError(t, err)
	if len(child) != 0 {
		t.Fatalf("bound sub-agent read %d rows its parent's binding already reads", len(child))
	}
	newer, err := readOpenCodeSteps(ctx, db, openCodeTestRoot, records[1].Offset, 10)
	mustNoError(t, err)
	if len(newer) != 0 {
		t.Fatalf("rows after cursor = %+v", newer)
	}
	if _, err := db.Exec(`DELETE FROM part`); err == nil {
		t.Fatal("usage database connection accepted a write")
	}
	after, err := os.ReadFile(path)
	mustNoError(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("reading the usage database modified it")
	}

	result := parseRecords(openCodeTestSource(domain.UsageSourceOpenCodeDB), records, records[1].Offset, time.Unix(1700000000, 0).UTC())
	if result.err != nil || len(result.Events) != 2 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	root := result.Events[0]
	if root.ProviderID != domain.UsageProviderAnthropic || root.BillingProviderID != "anthropic" ||
		root.BillingProviderSource != domain.UsageBillingProviderObserved ||
		root.ModelID != "claude-sonnet-4-5" || root.MeasurementKind != domain.UsageMeasurementNativeReported ||
		!root.CreatedAt.Equal(time.UnixMilli(openCodeTestTime).UTC()) {
		t.Fatalf("root event = %+v", root)
	}
	if tokenValue(root.Tokens.InputTokens) != 100 || tokenValue(root.Tokens.CachedInputTokens) != 20 ||
		tokenValue(root.Tokens.UncachedInputTokens) != 80 || tokenValue(root.Tokens.OutputTokens) != 20 {
		t.Fatalf("root tokens = %+v", root.Tokens)
	}
	if root.Costs.EstimatedCostNanos == nil || *root.Costs.EstimatedCostNanos != 12_500_000 ||
		root.Costs.PricingVersion != openCodePricingVersion {
		t.Fatalf("root costs = %+v, want OpenCode's reported cost", root.Costs)
	}
	if providerUsageTokens(t, root.ProviderUsageJSON, "tokens", "reasoning") != 5 {
		t.Fatalf("provider usage = %s", root.ProviderUsageJSON)
	}
	if providerUsageTokens(t, root.ProviderUsageJSON, derivedCacheWriteKey) != 10 {
		t.Fatalf("provider usage cache-write count missing: %s", root.ProviderUsageJSON)
	}
	sub := result.Events[1]
	if sub.ProviderID != domain.UsageProviderOpenAI || sub.BillingProviderID != "zai-coding-plan" ||
		tokenValue(sub.Tokens.InputTokens) != 22000 || tokenValue(sub.Tokens.CachedInputTokens) != 21824 ||
		tokenValue(sub.Tokens.OutputTokens) != 73 {
		t.Fatalf("sub-agent event = %+v", sub)
	}
	if sub.Costs.EstimatedCostNanos != nil {
		t.Fatalf("sub-agent costs = %+v, want a zero cost left to the catalog", sub.Costs)
	}
}

func TestReadOpenCodeStepsAfterUndo(t *testing.T) {
	path, writer := openCodeFixtureDB(t, "opencode.db")
	insertOpenCodeStep(t, writer, openCodeTestRoot, "a", "zai", "glm-4.5", openCodeTestTime, 10, 1, 0, 0, 0, 0)
	insertOpenCodeStep(t, writer, openCodeTestRoot, "b", "zai", "glm-4.5", openCodeTestTime+10, 20, 2, 0, 0, 0, 0)
	ctx := context.Background()
	db, err := openSQLiteUsageDB(ctx, path)
	mustNoError(t, err)
	defer func() { _ = db.Close() }()
	records, err := readOpenCodeSteps(ctx, db, openCodeTestRoot, 0, 10)
	mustNoError(t, err)
	if len(records) != 2 {
		t.Fatalf("records = %+v", records)
	}
	var undone int64
	mustNoError(t, writer.QueryRow(`SELECT rowid FROM part WHERE id = 'prt_b'`).Scan(&undone))
	_, err = writer.Exec(`DELETE FROM part WHERE id = 'prt_b'; DELETE FROM message WHERE id = 'msg_b'`)
	mustNoError(t, err)
	insertOpenCodeStep(t, writer, openCodeTestRoot, "c", "zai", "glm-4.5", openCodeTestTime+20, 30, 3, 0, 0, 0, 0)
	var reused int64
	mustNoError(t, writer.QueryRow(`SELECT rowid FROM part WHERE id = 'prt_c'`).Scan(&reused))
	if reused > undone {
		t.Fatalf("fixture did not reuse the undone row ID: %d after %d", reused, undone)
	}

	newer, err := readOpenCodeSteps(ctx, db, openCodeTestRoot, records[1].Offset, 10)
	mustNoError(t, err)
	if len(newer) != 1 || newer[0].Offset <= records[1].Offset {
		t.Fatalf("records after undo = %+v, want the new call", newer)
	}
}

func TestParseOpenCodeSkipsFailedAndMalformedSteps(t *testing.T) {
	records := []jsonlRecord{
		{Offset: 1, Data: []byte(`{"part_id":"prt_1","session_id":"ses_root","provider_id":"zai","model_id":"glm-4.5","step":{"type":"step-finish","tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0}}`)},
		{Offset: 2, Data: []byte(`{"part_id":"prt_2","session_id":"ses_root","provider_id":"zai","model_id":"glm-4.5","step":{"type":"step-finish","tokens":{"input":-1,"output":3,"reasoning":0,"cache":{"read":0,"write":0}}}}`)},
		{Offset: 3, Data: []byte(`{"part_id":"prt_3","session_id":"ses_root","provider_id":"zai","model_id":"","step":{"type":"step-finish","tokens":{"input":5,"output":3,"reasoning":0,"cache":{"read":0,"write":0}}}}`)},
		{Offset: 4, Data: []byte(`{"part_id":"prt_4","session_id":"ses_root","provider_id":"zai","model_id":"glm-4.5","step":{"type":"step-finish"}}`)},
	}
	result := parseRecords(openCodeTestSource(domain.UsageSourceOpenCodeDB), records, 4, time.Now())
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(result.Events) != 0 || result.Cursor.AnomalyCount != 3 {
		t.Fatalf("result = %+v", result)
	}
}

func TestParseKiloStepsKeepKiloCost(t *testing.T) {
	record := jsonlRecord{Offset: 1, Data: []byte(`{"part_id":"prt_1","session_id":"ses_root","time_created":1793465755757,"provider_id":"zai","model_id":"glm-4.5","step":{"reason":"stop","type":"step-finish","tokens":{"total":12650,"input":12593,"output":13,"reasoning":1,"cache":{"write":0,"read":43}},"cost":0.00759133}}`)}
	kilo := parseRecords(openCodeTestSource(domain.UsageSourceKiloDB), []jsonlRecord{record}, 1, time.Now())
	openCode := parseRecords(openCodeTestSource(domain.UsageSourceOpenCodeDB), []jsonlRecord{record}, 1, time.Now())
	if kilo.err != nil || len(kilo.Events) != 1 || openCode.err != nil || len(openCode.Events) != 1 {
		t.Fatalf("kilo = %+v, opencode = %+v", kilo, openCode)
	}
	event := kilo.Events[0]
	if event.BillingProviderID != "zai" || tokenValue(event.Tokens.InputTokens) != 12636 ||
		tokenValue(event.Tokens.CachedInputTokens) != 43 || tokenValue(event.Tokens.OutputTokens) != 14 {
		t.Fatalf("kilo event = %+v", event)
	}
	if event.Costs.EstimatedCostNanos == nil || *event.Costs.EstimatedCostNanos != 7_591_330 ||
		event.Costs.PricingVersion != kiloPricingVersion {
		t.Fatalf("kilo costs = %+v", event.Costs)
	}
	if event.SourceEventKey == openCode.Events[0].SourceEventKey {
		t.Fatal("Kilo and OpenCode share an event key namespace")
	}
}

func TestParseOpenCodeKeepsZeroCostOnOwnGateway(t *testing.T) {
	step := `"step":{"type":"step-finish","tokens":{"input":100,"output":2,"reasoning":0,"cache":{"read":0,"write":0}},"cost":0}`
	for _, tt := range []struct {
		kind     domain.UsageSourceKind
		provider string
		version  string
	}{
		{domain.UsageSourceOpenCodeDB, "opencode", openCodePricingVersion},
		{domain.UsageSourceKiloDB, "kilo", kiloPricingVersion},
	} {
		record := jsonlRecord{Offset: 1, Data: []byte(`{"part_id":"prt_1","session_id":"ses_root","time_created":1793465755757,"provider_id":"` + tt.provider + `","model_id":"free-model",` + step + `}`)}
		result := parseRecords(openCodeTestSource(tt.kind), []jsonlRecord{record}, 1, time.Now())
		if result.err != nil || len(result.Events) != 1 {
			t.Fatalf("%s result = %+v", tt.provider, result)
		}
		costs := result.Events[0].Costs
		if costs.EstimatedCostNanos == nil || *costs.EstimatedCostNanos != 0 || costs.PricingVersion != tt.version {
			t.Fatalf("%s costs = %+v, want a reported $0", tt.provider, costs)
		}
	}
	record := jsonlRecord{Offset: 1, Data: []byte(`{"part_id":"prt_1","session_id":"ses_root","time_created":1793465755757,"provider_id":"zai","model_id":"glm-4.5",` + step + `}`)}
	result := parseRecords(openCodeTestSource(domain.UsageSourceOpenCodeDB), []jsonlRecord{record}, 1, time.Now())
	if result.err != nil || len(result.Events) != 1 || result.Events[0].Costs.EstimatedCostNanos != nil {
		t.Fatalf("zai zero cost = %+v, want it left to the catalog", result)
	}
}

func TestIngestorReadsOpenCodeStepsIncrementally(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	path, writer := openCodeFixtureDB(t, "opencode.db")
	insertOpenCodeStep(t, writer, openCodeTestRoot, "a", "zai", "glm-4.5", openCodeTestTime, 100, 15, 5, 0, 0, 0.01)
	store, session := seedUsageTestSession(
		t, t.TempDir(), "usage", domain.HarnessOpenCode, domain.ActivityActive, openCodeTestRoot, now,
	)
	binding := seedUsageTestBinding(t, store, session, openCodeTestRoot, domain.UsageBindingActive, now)
	identity, err := usagesvc.SourceIdentity(ctx, path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceOpenCodeDB,
		NativeSessionID: openCodeTestRoot,
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

	_, err = writer.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES
    ('msg_streaming', 'ses_root', ?, ?, '{"role":"assistant","providerID":"zai","modelID":"glm-4.5","tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}}')`,
		openCodeTestTime+5, openCodeTestTime+5)
	mustNoError(t, err)
	_, err = ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	assertTokenAggregate(t, store, session.ID, 120)

	insertOpenCodeStep(t, writer, openCodeTestRoot, "b", "zai", "glm-4.5", openCodeTestTime+10, 40, 4, 0, 0, 0, 0)
	insertOpenCodeStep(t, writer, openCodeTestChild, "c", "zai", "glm-4.5", openCodeTestTime+20, 10, 1, 0, 0, 0, 0)
	_, err = ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	assertTokenAggregate(t, store, session.ID, 175)
	persisted, ok, err := store.GetUsageSourceForIngestion(ctx, source.ID)
	mustNoError(t, err)
	if !ok || persisted.Source.ByteOffset>>openCodeCursorTieBits != openCodeTestTime+20 ||
		persisted.Source.State != domain.UsageSourceActive {
		t.Fatalf("source cursor = %+v", persisted.Source)
	}
	_, err = ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	assertTokenAggregate(t, store, session.ID, 175)
}
