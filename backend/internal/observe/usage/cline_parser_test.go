package usage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const clineTestSessionID = "1793093930983_gfzbo"

func clineDocument(sessionID string, messages ...string) string {
	return `{
  "version": 1,
  "updated_at": "2026-11-03T10:00:00.000Z",
  "agent": "lead",
  "sessionId": "` + sessionID + `",
  "messages": [` + strings.Join(messages, ",\n") + `]
}
`
}

const clineUserMessage = `{"id":"u1","role":"user","content":"x","ts":1793093930000}`

const clineOpenRouterMessage = `{"id":"msg_a1","role":"assistant","content":"","ts":1793093956778,` +
	`"modelInfo":{"id":"stepfun/step-3.7-flash","provider":"openrouter"},` +
	`"metrics":{"inputTokens":5444,"outputTokens":41,"cacheReadTokens":1000,"cacheWriteTokens":0,"cost":0.0011927475}}`

const clineAnthropicMessage = `{"id":"msg_a2","role":"assistant","content":"","ts":1793093960000,` +
	`"modelInfo":{"id":"claude-sonnet-4-6","provider":"anthropic"},` +
	`"metrics":{"inputTokens":4700,"outputTokens":20,"cacheReadTokens":300,"cacheWriteTokens":4340,"cost":0.02}}`

const clineDisplayOnlyMessage = `{"id":"msg_err","role":"assistant","content":"","ts":1793093970000,` +
	`"metadata":{"displayOnly":true,"displayRole":"error"},"modelInfo":{"id":"glm-5.2","provider":"zai"}}`

// TestParseClineSnapshotMapsAssistantMetrics catches double-counting cached
// input: Cline's inputTokens is the whole prompt, cache reads and writes
// included.
func TestParseClineSnapshotMapsAssistantMetrics(t *testing.T) {
	source := usageSource(domain.UsageSourceClineSession)
	source.NativeRootID = clineTestSessionID
	document := clineDocument(clineTestSessionID,
		clineUserMessage, clineOpenRouterMessage, clineAnthropicMessage, clineDisplayOnlyMessage)

	result := parseSnapshot(source, []byte(document), time.Now())
	if result.err != nil || len(result.Events) != 2 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	openRouter, anthropic := result.Events[0], result.Events[1]
	if openRouter.ProviderID != domain.UsageProviderOpenAI || openRouter.ModelID != "stepfun/step-3.7-flash" ||
		openRouter.BillingProviderID != "openrouter" ||
		openRouter.BillingProviderSource != domain.UsageBillingProviderObserved ||
		openRouter.ProviderMessageID != "cline:msg_a1" ||
		openRouter.CreatedAt != time.UnixMilli(1793093956778).UTC() {
		t.Fatalf("openrouter event = %+v", openRouter)
	}
	if tokenValue(openRouter.Tokens.InputTokens) != 5444 ||
		tokenValue(openRouter.Tokens.CachedInputTokens) != 1000 ||
		tokenValue(openRouter.Tokens.UncachedInputTokens) != 4444 ||
		tokenValue(openRouter.Tokens.OutputTokens) != 41 {
		t.Fatalf("openrouter tokens = %+v", openRouter.Tokens)
	}
	if !strings.Contains(openRouter.ProviderUsageJSON, `"cost":0.0011927475`) {
		t.Fatalf("provider usage lost Cline's reported cost: %s", openRouter.ProviderUsageJSON)
	}
	if anthropic.ProviderID != domain.UsageProviderAnthropic || anthropic.BillingProviderID != "anthropic" ||
		tokenValue(anthropic.Tokens.InputTokens) != 4700 ||
		tokenValue(anthropic.Tokens.CachedInputTokens) != 300 ||
		tokenValue(anthropic.Tokens.UncachedInputTokens) != 4400 ||
		tokenValue(anthropic.Tokens.OutputTokens) != 20 {
		t.Fatalf("anthropic event = %+v tokens = %+v", anthropic, anthropic.Tokens)
	}
}

func TestParseClineSnapshotRejectsAnotherSessionsDocument(t *testing.T) {
	source := usageSource(domain.UsageSourceClineSession)
	source.NativeRootID = clineTestSessionID
	result := parseSnapshot(source, []byte(clineDocument("1793093930983_other", clineOpenRouterMessage)), time.Now())
	if len(result.Events) != 0 || result.Cursor.LastErrorCode != domain.UsageErrorArtifactPathRejected {
		t.Fatalf("result = %+v", result)
	}
}

func TestParseClineSnapshotRejectsCacheExceedingInput(t *testing.T) {
	source := usageSource(domain.UsageSourceClineSession)
	source.NativeRootID = clineTestSessionID
	bad := `{"id":"msg_bad","role":"assistant","modelInfo":{"id":"gpt-5","provider":"openai"},` +
		`"metrics":{"inputTokens":10,"outputTokens":1,"cacheReadTokens":8,"cacheWriteTokens":5}}`
	result := parseSnapshot(source, []byte(clineDocument(clineTestSessionID, bad)), time.Now())
	if len(result.Events) != 0 || result.Cursor.AnomalyCount != 1 ||
		result.Cursor.LastErrorCode != domain.UsageErrorMalformedJSONL {
		t.Fatalf("result = %+v", result)
	}
}

func seedClineSnapshotSource(t *testing.T, dataDir string) (*sqlite.Store, domain.UsageSourceRecord, string, time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	store, session := seedUsageTestSession(
		t, dataDir, "usage", domain.HarnessCline, domain.ActivityIdle, clineTestSessionID, now,
	)
	binding := seedUsageTestBinding(t, store, session, clineTestSessionID, domain.UsageBindingActive, now)
	path := filepath.Join(t.TempDir(), clineTestSessionID+".messages.json")
	mustNoError(t, os.WriteFile(path, []byte(clineDocument(clineTestSessionID, clineUserMessage)), 0o600))
	path = canonicalTranscriptPath(path)
	identity, err := usagesvc.SourceIdentity(ctx, path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceClineSession,
		NativeSessionID: clineTestSessionID,
		ArtifactPath:    path,
		FileIdentity:    identity,
		State:           domain.UsageSourcePending,
		UpdatedAt:       now,
	})
	mustNoError(t, err)
	return store, source, path, now
}

func countSourceEvents(t *testing.T, dataDir string, bindingID int64) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db"))
	mustNoError(t, err)
	defer func() { _ = db.Close() }()
	var count int
	mustNoError(t, db.QueryRow(`SELECT COUNT(*) FROM model_usage_events WHERE binding_id = ?`, bindingID).Scan(&count))
	return count
}

// TestIngestorSnapshotCountsRewrittenDocumentOnce catches the two ways
// byte-offset tailing fails on a document Cline rewrites in place: counting
// every message again on each rewrite, and losing new messages after a rewrite
// shrinks the file below the old offset.
func TestIngestorSnapshotCountsRewrittenDocumentOnce(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedClineSnapshotSource(t, dataDir)
	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})

	mustNoError(t, os.WriteFile(path, []byte(clineDocument(clineTestSessionID,
		clineUserMessage, clineOpenRouterMessage)+strings.Repeat(" ", 4096)), 0o600))
	if _, err := ingestor.Ingest(ctx, source.ID); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if got := countSourceEvents(t, dataDir, source.BindingID); got != 1 {
		t.Fatalf("events after first write = %d, want 1", got)
	}

	// A shorter rewrite that also adds a message.
	mustNoError(t, os.WriteFile(path, []byte(clineDocument(clineTestSessionID,
		clineUserMessage, clineOpenRouterMessage, clineAnthropicMessage)), 0o600))
	if _, err := ingestor.Ingest(ctx, source.ID); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if got := countSourceEvents(t, dataDir, source.BindingID); got != 2 {
		t.Fatalf("events after rewrite = %d, want 2", got)
	}
	stored, ok, err := store.GetUsageSourceForIngestion(ctx, source.ID)
	mustNoError(t, err)
	if !ok || stored.Source.ByteOffset != 0 || stored.Source.AnomalyCount != 0 ||
		stored.Source.LastErrorCode != "" || !strings.Contains(stored.Source.ParserStateJSON, `"snapshot"`) {
		t.Fatalf("source after rewrite = %+v", stored.Source)
	}

	// A read that lands mid-write leaves the cursor and events alone.
	mustNoError(t, os.WriteFile(path, []byte(`{"version":1,"sessionId":"`+clineTestSessionID+`","messages":[`), 0o600))
	if _, err := ingestor.Ingest(ctx, source.ID); err == nil {
		t.Fatal("ingesting a torn document succeeded")
	}
	torn, _, err := store.GetUsageSourceForIngestion(ctx, source.ID)
	mustNoError(t, err)
	if torn.Source.ParserStateJSON != stored.Source.ParserStateJSON {
		t.Fatalf("torn read advanced parser state: %s", torn.Source.ParserStateJSON)
	}
	if got := countSourceEvents(t, dataDir, source.BindingID); got != 2 {
		t.Fatalf("events after torn read = %d, want 2", got)
	}
}
