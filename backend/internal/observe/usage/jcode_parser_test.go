package usage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

const testJCodeLedgerSource domain.UsageSourceKind = "jcode_ledger"

// The token_usage object is copied from a real jcode v0.99.1 Claude session:
// prompt_tokens = input_tokens (uncached) + cache_read + cache_creation.
func TestParseJCodeLedgerAnthropicUsage(t *testing.T) {
	source := usageSource(testJCodeLedgerSource)
	source.NativeRootID = "session_elephant_1793375224099_dccf0b8667353cd1"
	records := []jsonlRecord{
		{Offset: 0, Data: []byte(`{"type":"jcode.usage","id":"message_1793375281634_1729629423521544423","session_id":"session_elephant_1793375224099_dccf0b8667353cd1","timestamp":"2026-10-30T15:41:21.634Z","model":"claude-opus-5-5","provider":"claude","token_usage":{"prompt_tokens":26544,"input_tokens":423,"output_tokens":225,"cache_read_input_tokens":18804,"cache_creation_input_tokens":7317}}`)},
		{Offset: 400, Data: []byte(`{"type":"jcode.usage","id":"message_1793375283277_7015021863367367151","session_id":"session_elephant_1793375224099_dccf0b8667353cd1","timestamp":"2026-10-30T15:41:23.277Z","model":"claude-opus-5-5","provider":"claude","token_usage":{"prompt_tokens":26830,"input_tokens":61,"output_tokens":108,"cache_read_input_tokens":26121,"cache_creation_input_tokens":648}}`)},
	}
	result := parseRecords(source, records, 800, time.Unix(1700000000, 0).UTC())
	if result.err != nil || len(result.Events) != 2 {
		t.Fatalf("result = %+v", result)
	}
	first := result.Events[0]
	if first.ProviderID != domain.UsageProviderAnthropic || first.ModelID != "claude-opus-5-5" ||
		first.MeasurementKind != domain.UsageMeasurementNativeReported ||
		first.CreatedAt != time.Date(2026, 10, 30, 15, 41, 21, 634000000, time.UTC) {
		t.Fatalf("event = %+v", first)
	}
	// Cached reads are part of the input total exactly once.
	if tokenValue(first.Tokens.InputTokens) != 26544 || tokenValue(first.Tokens.CachedInputTokens) != 18804 ||
		tokenValue(first.Tokens.UncachedInputTokens) != 7740 || tokenValue(first.Tokens.OutputTokens) != 225 {
		t.Fatalf("tokens = %+v", first.Tokens)
	}
	if result.Events[0].SourceEventKey == result.Events[1].SourceEventKey {
		t.Fatal("distinct native messages share one event key")
	}
}

// For providers whose prompt count already includes cached tokens
// (prompt_tokens == input_tokens), the cache must not be added twice.
func TestParseJCodeLedgerPromptIncludesCache(t *testing.T) {
	source := usageSource(testJCodeLedgerSource)
	source.NativeRootID = "session_fox_1793367230599_7049d57744447aef"
	record := jsonlRecord{Offset: 0, Data: []byte(`{"type":"jcode.usage","id":"message_1","session_id":"session_fox_1793367230599_7049d57744447aef","timestamp":"2026-10-30T15:00:00Z","model":"gpt-5.5","provider":"openai","token_usage":{"prompt_tokens":1000,"input_tokens":1000,"output_tokens":50,"cache_read_input_tokens":600}}`)}
	result := parseRecords(source, []jsonlRecord{record}, 300, time.Unix(1700000000, 0).UTC())
	if result.err != nil || len(result.Events) != 1 {
		t.Fatalf("result = %+v", result)
	}
	event := result.Events[0]
	if event.ProviderID != domain.UsageProviderOpenAI || tokenValue(event.Tokens.InputTokens) != 1000 ||
		tokenValue(event.Tokens.CachedInputTokens) != 600 || tokenValue(event.Tokens.UncachedInputTokens) != 400 ||
		tokenValue(event.Tokens.OutputTokens) != 50 {
		t.Fatalf("event = %+v tokens = %+v", event, event.Tokens)
	}
}

// Re-reading the same ledger line (a reactivated or restored session) must
// produce the same key, so the store's unique key keeps lifetime totals once.
func TestParseJCodeLedgerReplayStableAndDeduplicated(t *testing.T) {
	source := usageSource(testJCodeLedgerSource)
	source.NativeRootID = "session_owl_1793367230599_0000000000000001"
	line := []byte(`{"type":"jcode.usage","id":"message_7","session_id":"session_owl_1793367230599_0000000000000001","timestamp":"2026-10-30T15:00:00Z","model":"claude-opus-5-5","provider":"claude","token_usage":{"prompt_tokens":30,"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":20,"cache_creation_input_tokens":0}}`)
	first := parseRecords(source, []jsonlRecord{{Offset: 0, Data: line}, {Offset: 500, Data: line}}, 900, time.Unix(1700000000, 0).UTC())
	second := parseRecords(source, []jsonlRecord{{Offset: 0, Data: line}}, 500, time.Unix(1700000001, 0).UTC())
	if first.err != nil || len(first.Events) != 1 || len(second.Events) != 1 ||
		first.Events[0].SourceEventKey != second.Events[0].SourceEventKey {
		t.Fatalf("first=%+v second=%+v", first.Events, second.Events)
	}
}

func TestParseJCodeLedgerRejectsInconsistentCounters(t *testing.T) {
	source := usageSource(testJCodeLedgerSource)
	source.NativeRootID = "session_owl_1793367230599_0000000000000001"
	records := []jsonlRecord{
		// prompt smaller than its own cached part
		{Offset: 0, Data: []byte(`{"type":"jcode.usage","id":"m1","timestamp":"2026-10-30T15:00:00Z","model":"claude-opus-5-5","provider":"claude","token_usage":{"prompt_tokens":5,"input_tokens":5,"output_tokens":1,"cache_read_input_tokens":20}}`)},
		// no usage object
		{Offset: 200, Data: []byte(`{"type":"jcode.usage","id":"m2","timestamp":"2026-10-30T15:00:00Z","model":"claude-opus-5-5","provider":"claude","token_usage":null}`)},
		{Offset: 400, Data: []byte(`not json`)},
	}
	result := parseRecords(source, records, 600, time.Unix(1700000000, 0).UTC())
	if result.err != nil || len(result.Events) != 0 || result.Cursor.AnomalyCount == 0 {
		t.Fatalf("result = %+v", result)
	}
}

// TestIngestorCountsJCodeLedgerOncePerMessage drives the real ingestor and
// store: re-ingesting after a restart, or a duplicated ledger line from a
// resumed session, must not re-add a message's tokens.
func TestIngestorCountsJCodeLedgerOncePerMessage(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	const nativeID = "session_elephant_1793375224099_dccf0b8667353cd1"
	store, session := seedUsageTestSession(t, t.TempDir(), "usage", domain.HarnessJCode, domain.ActivityActive, nativeID, now)
	binding := seedUsageTestBinding(t, store, session, nativeID, domain.UsageBindingActive, now)
	path := t.TempDir() + "/" + nativeID + ".jsonl"
	first := `{"type":"jcode.usage","id":"message_2","session_id":"` + nativeID + `","timestamp":"2026-10-30T15:41:21.634Z","model":"claude-opus-5-5","provider":"claude","token_usage":{"prompt_tokens":26544,"input_tokens":423,"output_tokens":225,"cache_read_input_tokens":18804,"cache_creation_input_tokens":7317}}` + "\n"
	second := `{"type":"jcode.usage","id":"message_4","session_id":"` + nativeID + `","timestamp":"2026-10-30T15:41:23.277Z","model":"claude-opus-5-5","provider":"claude","token_usage":{"prompt_tokens":26830,"input_tokens":61,"output_tokens":108,"cache_read_input_tokens":26121,"cache_creation_input_tokens":648}}` + "\n"
	mustNoError(t, os.WriteFile(path, []byte(first), 0o600))
	path = canonicalTranscriptPath(path)
	identity, err := usagesvc.SourceIdentity(ctx, path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID: binding.ID, Kind: domain.UsageSourceJCodeLedger, NativeSessionID: nativeID,
		ArtifactPath: path, FileIdentity: identity, State: domain.UsageSourcePending, UpdatedAt: now,
	})
	mustNoError(t, err)
	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})
	ingestSourceFully(ctx, t, ingestor, source.ID)
	assertTokenAggregate(t, store, session.ID, 26544+225)

	mustNoError(t, osAppend(path, second+first))
	now = now.Add(30 * time.Second)
	ingestSourceFully(ctx, t, ingestor, source.ID)
	assertTokenAggregate(t, store, session.ID, 26544+225+26830+108)

	restarted := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})
	ingestSourceFully(ctx, t, restarted, source.ID)
	assertTokenAggregate(t, store, session.ID, 26544+225+26830+108)
}
