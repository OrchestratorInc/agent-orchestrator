package usage

import (
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestParseGeminiCountsEachMessageOnce catches double counting: Gemini CLI
// appends a message again whenever it changes, first without tokens and then
// with them, and again after each tool call update.
func TestParseGeminiCountsEachMessageOnce(t *testing.T) {
	source := usageSource(domain.UsageSourceGeminiSession)
	source.NativeRootID = "gemini-session"
	records := []jsonlRecord{
		{Data: []byte(`{"sessionId":"gemini-session","projectHash":"abc","startTime":"2026-11-03T10:00:00Z","lastUpdated":"2026-11-03T10:00:00Z"}`)},
		{Offset: 100, Data: []byte(`{"id":"user-1","timestamp":"2026-11-03T10:00:01Z","type":"user","content":[{"text":"x"}]}`)},
		{Offset: 200, Data: []byte(`{"id":"msg-1","timestamp":"2026-11-03T10:00:02Z","type":"gemini","content":"","model":"gemini-2.5-pro"}`)},
		{Offset: 300, Data: []byte(`{"id":"msg-1","timestamp":"2026-11-03T10:00:02Z","type":"gemini","content":"","model":"gemini-2.5-pro","tokens":{"input":120,"output":30,"cached":40,"thoughts":8,"tool":5,"total":163}}`)},
		{Offset: 400, Data: []byte(`{"$set":{"lastUpdated":"2026-11-03T10:00:03Z"}}`)},
		{Offset: 500, Data: []byte(`{"id":"msg-1","timestamp":"2026-11-03T10:00:02Z","type":"gemini","content":"","model":"gemini-2.5-pro","tokens":{"input":120,"output":30,"cached":40,"thoughts":8,"tool":5,"total":163},"toolCalls":[{"id":"t1","name":"read_file","status":"success"}]}`)},
		{Offset: 600, Data: []byte(`{"$patch":{"id":"msg-1","content":"done"}}`)},
		{Offset: 700, Data: []byte(`{"$rewindTo":"user-1"}`)},
	}

	result := parseRecords(source, records, 800, time.Unix(1700000000, 0).UTC())
	if result.err != nil || len(result.Events) != 1 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	event := result.Events[0]
	if event.ProviderID != domain.UsageProviderOpenAI || event.ModelID != "gemini-2.5-pro" ||
		event.BillingProviderID != "" || event.MeasurementKind != domain.UsageMeasurementNativeReported ||
		event.CreatedAt != time.Date(2026, 11, 3, 10, 0, 2, 0, time.UTC) || event.SourceEventKey == "" {
		t.Fatalf("event = %+v", event)
	}
	// The tool-use prompt is extra input and thoughts are extra output;
	// cached content is already part of Gemini's prompt count.
	if tokenValue(event.Tokens.InputTokens) != 125 ||
		tokenValue(event.Tokens.CachedInputTokens) != 40 ||
		tokenValue(event.Tokens.UncachedInputTokens) != 85 ||
		tokenValue(event.Tokens.OutputTokens) != 38 {
		t.Fatalf("tokens = %+v", event.Tokens)
	}
	if providerUsageTokens(t, event.ProviderUsageJSON, "total") != 163 {
		t.Fatalf("provider usage = %s", event.ProviderUsageJSON)
	}
}

// TestParseGeminiKeysEventsBySessionAndMessage catches a replayed transcript
// minting new keys, and two messages collapsing into one.
func TestParseGeminiKeysEventsBySessionAndMessage(t *testing.T) {
	source := usageSource(domain.UsageSourceGeminiSession)
	source.NativeRootID = "gemini-session"
	first := jsonlRecord{Data: []byte(`{"id":"msg-1","type":"gemini","model":"gemini-2.5-flash","tokens":{"input":10,"output":2,"cached":0,"total":12}}`)}
	second := jsonlRecord{Offset: 100, Data: []byte(`{"id":"msg-2","type":"gemini","model":"gemini-2.5-flash","tokens":{"input":20,"output":3,"cached":5,"total":23}}`)}

	initial := parseRecords(source, []jsonlRecord{first, second}, 200, time.Now())
	replayed := parseRecords(source, []jsonlRecord{{Offset: 0, Data: second.Data}}, 100, time.Now())
	if len(initial.Events) != 2 || len(replayed.Events) != 1 {
		t.Fatalf("initial = %+v replayed = %+v", initial, replayed)
	}
	if initial.Events[0].SourceEventKey == initial.Events[1].SourceEventKey ||
		initial.Events[1].SourceEventKey != replayed.Events[0].SourceEventKey {
		t.Fatalf("keys = %q %q replayed %q", initial.Events[0].SourceEventKey,
			initial.Events[1].SourceEventKey, replayed.Events[0].SourceEventKey)
	}
}

func TestParseGeminiRejectsMalformedTokens(t *testing.T) {
	source := usageSource(domain.UsageSourceGeminiSession)
	source.NativeRootID = "gemini-session"
	for name, data := range map[string]string{
		"cached exceeds input": `{"id":"m","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":3,"output":1,"cached":4,"total":4}}`,
		"negative output":      `{"id":"m","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":3,"output":-1,"cached":0,"total":2}}`,
		"missing model":        `{"id":"m","type":"gemini","tokens":{"input":3,"output":1,"cached":0,"total":4}}`,
		"tokens not an object": `{"id":"m","type":"gemini","model":"gemini-2.5-pro","tokens":7}`,
	} {
		t.Run(name, func(t *testing.T) {
			result := parseRecords(source, []jsonlRecord{{Data: []byte(data)}}, 100, time.Now())
			if result.err != nil || len(result.Events) != 0 || result.Cursor.AnomalyCount != 1 ||
				result.Cursor.LastErrorCode != domain.UsageErrorMalformedJSONL {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

// TestParseGeminiSkipsRestatementInLaterChunk catches a message re-appended
// after its first counted line was already applied: replaying it would collide
// with the stored event once repair has attributed that event.
func TestParseGeminiSkipsRestatementInLaterChunk(t *testing.T) {
	source := usageSource(domain.UsageSourceGeminiSession)
	source.NativeRootID = "gemini-session"
	line := `{"id":"msg-1","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":10,"output":2,"cached":0,"total":12}}`
	state, err := newParserState(source.Source.Kind)
	if err != nil {
		t.Fatal(err)
	}
	first := parseRecordsWithState(source, []jsonlRecord{{Data: []byte(line)}}, 100, time.Now(), state)
	if first.err != nil || len(first.Events) != 1 {
		t.Fatalf("first = %+v", first)
	}
	source.Source.ParserStateJSON = first.Cursor.ParserStateJSON
	decoded, err := decodeParserState(source.Source)
	if err != nil {
		t.Fatalf("decode state %s: %v", first.Cursor.ParserStateJSON, err)
	}
	restated := `{"id":"msg-1","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":10,"output":2,"cached":0,"total":12},"toolCalls":[{"id":"t1"}]}`
	second := parseRecordsWithState(source, []jsonlRecord{
		{Offset: 100, Data: []byte(restated)},
		{Offset: 200, Data: []byte(`{"id":"msg-2","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":4,"output":1,"cached":0,"total":5}}`)},
	}, 300, time.Now(), decoded)
	if second.err != nil || len(second.Events) != 1 || second.Cursor.AnomalyCount != 0 {
		t.Fatalf("second = %+v", second)
	}
	if tokenValue(second.Events[0].Tokens.InputTokens) != 4 {
		t.Fatalf("second event = %+v", second.Events[0])
	}
}

// TestParseGeminiStateForgetsOldestMessages keeps parser state bounded on a
// long session.
func TestParseGeminiStateForgetsOldestMessages(t *testing.T) {
	source := usageSource(domain.UsageSourceGeminiSession)
	source.NativeRootID = "gemini-session"
	records := make([]jsonlRecord, 0, maxGeminiCountedMessageIDs+5)
	for index := range maxGeminiCountedMessageIDs + 5 {
		records = append(records, jsonlRecord{
			Offset: int64(index * 100),
			Data:   []byte(fmt.Sprintf(`{"id":"msg-%d","type":"gemini","model":"gemini-2.5-pro","tokens":{"input":1,"output":1,"cached":0,"total":2}}`, index)),
		})
	}
	state, err := newParserState(source.Source.Kind)
	if err != nil {
		t.Fatal(err)
	}
	result := parseRecordsWithState(source, records, int64(len(records)*100), time.Now(), state)
	if result.err != nil || len(result.Events) != len(records) {
		t.Fatalf("events = %d, want %d", len(result.Events), len(records))
	}
	if got := len(state.Gemini.CountedMessageIDs); got != maxGeminiCountedMessageIDs ||
		state.Gemini.CountedMessageIDs[0] != "msg-5" {
		t.Fatalf("counted = %d first %q", got, state.Gemini.CountedMessageIDs[0])
	}
	source.Source.ParserStateJSON = result.Cursor.ParserStateJSON
	if _, err := decodeParserState(source.Source); err != nil {
		t.Fatalf("decode bounded state: %v", err)
	}
}
