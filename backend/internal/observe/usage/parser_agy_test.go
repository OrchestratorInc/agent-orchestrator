package usage

import (
	"context"
	"database/sql"
	"encoding/binary"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

const agyTestConversationID = "7e58d1c1-4a55-4e47-8393-fb6413f594fe"

func agyVarint(number, value uint64) []byte {
	out := binary.AppendUvarint(nil, number<<3)
	return binary.AppendUvarint(out, value)
}

func agyBytes(number uint64, value []byte) []byte {
	out := binary.AppendUvarint(nil, number<<3|2)
	out = binary.AppendUvarint(out, uint64(len(value)))
	return append(out, value...)
}

func agyConcat(parts ...[]byte) []byte {
	var out []byte
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

// agyGeneration builds a gen_metadata blob as the agy CLI writes it. A zero
// configName leaves the request config without the model's name, as on every
// call after a conversation's first.
func agyUsage(model, input, output, cacheRead, thinking uint64, responseID string) []byte {
	usage := agyConcat(agyVarint(1, model), agyVarint(2, input), agyVarint(3, output))
	if cacheRead > 0 {
		usage = append(usage, agyVarint(5, cacheRead)...)
	}
	return agyConcat(usage, agyVarint(6, 24), agyBytes(7, []byte("bot-"+responseID)),
		agyVarint(9, thinking), agyVarint(10, output-thinking), agyBytes(11, []byte(responseID)))
}

// agyGeneration builds a gen_metadata blob: the call's usage, repeated in its
// retry_infos as agy writes it, plus any earlier attempts that billed apart.
func agyGeneration(model, input, output, cacheRead, thinking uint64, responseID, configName string, retries ...[]byte) []byte {
	usage := agyUsage(model, input, output, cacheRead, thinking, responseID)
	chat := agyBytes(4, usage)
	for _, retry := range append([][]byte{usage}, retries...) {
		chat = append(chat, agyBytes(17, agyBytes(2, retry))...)
	}
	config := agyVarint(1, model)
	if configName != "" {
		config = append(config, agyBytes(28, []byte(configName))...)
	}
	return agyConcat(agyBytes(1, chat), agyBytes(3, config))
}

// agyStep builds a steps.metadata blob recording the response at a time.
func agyStep(responseID string, at time.Time) []byte {
	stamp := agyConcat(agyVarint(1, uint64(at.Unix())), agyVarint(2, uint64(at.Nanosecond())))
	return agyConcat(agyBytes(1, stamp), agyBytes(8, stamp), agyBytes(9, agyBytes(11, []byte(responseID))))
}

var agyTestStepAt = time.Date(2026, 11, 4, 11, 6, 3, 250_000_000, time.UTC)

// agyFixtureDB creates a conversation database whose first call's response has
// a step at agyTestStepAt, and returns an insert for its gen_metadata rows.
func agyFixtureDB(t *testing.T) (string, func(idx int64, data []byte)) {
	t.Helper()
	path := filepath.Join(t.TempDir(), agyTestConversationID+".db")
	writer, err := sql.Open("sqlite", path)
	mustNoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	_, err = writer.Exec(`CREATE TABLE gen_metadata (idx integer, data blob, size integer NOT NULL DEFAULT 0, PRIMARY KEY (idx))`)
	mustNoError(t, err)
	_, err = writer.Exec(`CREATE TABLE steps (idx integer, step_type integer NOT NULL DEFAULT 0, metadata blob, PRIMARY KEY (idx))`)
	mustNoError(t, err)
	_, err = writer.Exec(`INSERT INTO steps (idx, step_type, metadata) VALUES (0, 15, ?)`, agyStep("yFLbauGaL", agyTestStepAt))
	mustNoError(t, err)
	return path, func(idx int64, data []byte) {
		t.Helper()
		_, err := writer.Exec(`INSERT INTO gen_metadata (idx, data, size) VALUES (?, ?, ?)`, idx, data, len(data))
		mustNoError(t, err)
	}
}

// Break caught: an Antigravity conversation read as JSON, or its later calls
// left without a model. The agy CLI keeps one protobuf gen_metadata row per
// call and names the model only on the conversation's first calls, so the
// reader learns the name from those, prices cache reads as input, strips the
// thinking level for the catalog, counts a retry once unless it billed apart,
// takes each call's time from its step (or from the call before it, so no call
// is left out of every report period), waits on a newest row without usage,
// and never skips the conversation's first call, whose idx is 0.
func TestReadAndParseAgyConversation(t *testing.T) {
	path, insert := agyFixtureDB(t)
	stepAt := agyTestStepAt
	insert(0, agyGeneration(1318, 8214, 532, 0, 485, "yFLbauGaL", "gemini-3.8-flash-high"))
	insert(1, agyGeneration(1318, 7678, 75, 16627, 19, "2VLbaordO", "",
		agyUsage(1318, 7000, 0, 0, 0, "failedFirstTry")))
	insert(2, agyGeneration(4242, 100, 10, 0, 0, "unknownModel", ""))
	insert(3, agyConcat(agyBytes(3, agyVarint(1, 1318)))) // still streaming

	ctx := context.Background()
	db, err := openSQLiteUsageDB(ctx, path)
	mustNoError(t, err)
	defer func() { _ = db.Close() }()
	records, err := readAgyGenerations(ctx, db, agyTestConversationID, 0, 10)
	mustNoError(t, err)
	if len(records) != 4 || records[0].Offset != 1 || records[2].Offset != 2 || records[3].Offset != 3 {
		t.Fatalf("records = %+v, want the newest usage-less row held back", records)
	}
	newer, err := readAgyGenerations(ctx, db, agyTestConversationID, 3, 10)
	mustNoError(t, err)
	if len(newer) != 0 {
		t.Fatalf("records after cursor 3 = %+v, want none until the newest row has usage", newer)
	}

	source := usageSource(domain.UsageSourceAgyConversation)
	source.NativeRootID = agyTestConversationID
	result := parseRecords(source, records, records[3].Offset, time.Now())
	if result.err != nil || len(result.Events) != 4 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	first, cached, retry, unknown := result.Events[0], result.Events[1], result.Events[2], result.Events[3]
	for _, event := range result.Events {
		if !event.CreatedAt.Equal(stepAt) {
			t.Fatalf("call %s at %v, want the step's time or the previous call's", event.SourceEventKey, event.CreatedAt)
		}
	}
	// A later read whose calls match no step takes the conversation's latest.
	later, err := readAgyGenerations(ctx, db, agyTestConversationID, 1, 10)
	mustNoError(t, err)
	laterResult := parseRecords(source, later, later[len(later)-1].Offset, time.Now())
	if len(laterResult.Events) != 3 {
		t.Fatalf("later events = %+v", laterResult.Events)
	}
	for _, event := range laterResult.Events {
		if !event.CreatedAt.Equal(stepAt) {
			t.Fatalf("unstamped call %s at %v, want the latest step's time", event.SourceEventKey, event.CreatedAt)
		}
	}
	if retry.ProviderMessageID != "failedFirstTry" || tokenValue(retry.Tokens.InputTokens) != 7000 {
		t.Fatalf("separately billed retry = %+v", retry)
	}
	if first.ModelID != "gemini-3.8-flash" || first.BillingProviderID != "gemini" ||
		first.ProviderID != domain.UsageProviderOpenAI || tokenValue(first.Tokens.InputTokens) != 8214 ||
		tokenValue(first.Tokens.OutputTokens) != 532 || first.ProviderMessageID != "yFLbauGaL" {
		t.Fatalf("first call = %+v", first)
	}
	if cached.ModelID != "gemini-3.8-flash" || tokenValue(cached.Tokens.InputTokens) != 24305 ||
		tokenValue(cached.Tokens.CachedInputTokens) != 16627 || tokenValue(cached.Tokens.UncachedInputTokens) != 7678 {
		t.Fatalf("cached call = %+v", cached)
	}
	if unknown.ModelID != "agy-model-4242" || unknown.BillingProviderID != "" ||
		first.SourceEventKey == cached.SourceEventKey {
		t.Fatalf("unknown-model call = %+v", unknown)
	}
}

// Break caught: a gen_metadata row whose call also billed a retry apart yields
// two records with the row's cursor. The SQLite source rejected the repeated
// cursor as out of order and retried the same read forever, so the
// conversation stored nothing, its earlier rows included.
func TestIngestorReadsAgyRowWithSeparatelyBilledRetry(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	path, insert := agyFixtureDB(t)
	insert(0, agyGeneration(1318, 8214, 532, 0, 485, "yFLbauGaL", "gemini-3.8-flash-high"))
	insert(1, agyGeneration(1318, 7678, 75, 16627, 19, "2VLbaordO", "",
		agyUsage(1318, 7000, 0, 0, 0, "failedFirstTry")))
	store, session := seedUsageTestSession(
		t, t.TempDir(), "usage", domain.HarnessAgy, domain.ActivityActive, agyTestConversationID, now,
	)
	binding := seedUsageTestBinding(t, store, session, agyTestConversationID, domain.UsageBindingActive, now)
	identity, err := usagesvc.SourceIdentity(ctx, path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceAgyConversation,
		NativeSessionID: agyTestConversationID,
		ArtifactPath:    path,
		FileIdentity:    identity,
		State:           domain.UsageSourcePending,
		UpdatedAt:       now,
	})
	mustNoError(t, err)
	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})

	_, err = ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	// 8214+532, then 7678+16627+75 and the retry's 7000.
	assertTokenAggregate(t, store, session.ID, 40126)
	persisted, ok, err := store.GetUsageSourceForIngestion(ctx, source.ID)
	mustNoError(t, err)
	if !ok || persisted.Source.ByteOffset != 2 || persisted.Source.LastErrorCode != "" {
		t.Fatalf("source cursor = %+v, want past both rows with no error", persisted.Source)
	}
}
