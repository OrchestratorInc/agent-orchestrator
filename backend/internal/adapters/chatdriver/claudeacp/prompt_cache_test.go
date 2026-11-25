package claudeacp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func promptCacheTranscript(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

const (
	cacheUser      = `{"type":"user","timestamp":"2026-11-25T10:00:00.000Z","message":{"content":"hi"}}`
	cacheAssistant = `{"type":"assistant","timestamp":"2026-11-25T10:00:40.000Z","message":{"model":"claude-fable-5-2","usage":{"input_tokens":3,"cache_read_input_tokens":400000,"cache_creation_input_tokens":2000,"cache_creation":{"ephemeral_1h_input_tokens":2000,"ephemeral_5m_input_tokens":0}}}}`
	cacheToolUse   = `{"type":"user","timestamp":"2026-11-25T10:01:00.000Z","message":{"content":[{"type":"tool_result"}]}}`
	// Served fully from cache: no entry written, so no lifetime reported.
	cacheReadOnly  = `{"type":"assistant","timestamp":"2026-11-25T10:01:05.000Z","message":{"model":"claude-fable-5-2","usage":{"input_tokens":1,"cache_read_input_tokens":402003,"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0}}}}`
	cacheSynthetic = `{"type":"assistant","timestamp":"2026-11-25T10:02:00.000Z","message":{"model":"<synthetic>","usage":{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`
	cacheBoundary  = `{"type":"system","subtype":"compact_boundary","timestamp":"2026-11-25T10:03:00.000Z","compactMetadata":{"trigger":"manual","preTokens":402004,"postTokens":31000}}`
)

func TestPromptCacheStateDatesTheRequestFromItsPrompt(t *testing.T) {
	state, found := readPromptCacheState(context.Background(), strings.NewReader(promptCacheTranscript(cacheUser, cacheAssistant)), false)
	if !found {
		t.Fatal("no request found")
	}
	// The cache entry dates from when the provider read the prompt, not from
	// when the 40-second response finished.
	if want := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC); !state.LastRequestAt.Equal(want) {
		t.Errorf("LastRequestAt = %v, want %v", state.LastRequestAt, want)
	}
	if state.TTL != time.Hour || state.ContextTokens != 402003 {
		t.Errorf("state = %+v, want 1h and 402003 tokens", state)
	}
}

func TestPromptCacheStateKeepsTheLifetimeAcrossCacheOnlyRequests(t *testing.T) {
	state, found := readPromptCacheState(context.Background(), strings.NewReader(promptCacheTranscript(cacheUser, cacheAssistant, cacheToolUse, cacheReadOnly, cacheSynthetic)), false)
	if !found {
		t.Fatal("no request found")
	}
	if want := time.Date(2026, 11, 25, 10, 1, 0, 0, time.UTC); !state.LastRequestAt.Equal(want) {
		t.Errorf("LastRequestAt = %v, want the tool result's %v (synthetic records are not requests)", state.LastRequestAt, want)
	}
	if state.TTL != time.Hour {
		t.Errorf("TTL = %v, want the earlier request's 1h", state.TTL)
	}
}

func TestPromptCacheStateAfterCompactionReportsTheSummarySize(t *testing.T) {
	state, found := readPromptCacheState(context.Background(), strings.NewReader(promptCacheTranscript(cacheUser, cacheAssistant, cacheBoundary)), false)
	if !found || state.ContextTokens != 31000 {
		t.Fatalf("state = %+v found=%v, want the 31000-token summary", state, found)
	}
}

func TestPromptCacheStateSkipsAPartialFirstLine(t *testing.T) {
	partial := cacheAssistant[len(cacheAssistant)/2:]
	if _, found := readPromptCacheState(context.Background(), strings.NewReader(promptCacheTranscript(partial)), true); found {
		t.Fatal("a cut-off record was read as a request")
	}
	if _, found := readPromptCacheState(context.Background(), strings.NewReader(""), false); found {
		t.Fatal("an empty transcript reported a request")
	}
}
