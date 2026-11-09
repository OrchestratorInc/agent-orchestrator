package usage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const deepseekTestSessionID = "bbeef2fa-f748-4ed4-bd69-73f91f71f8dc"

var deepseekTestFrames = []string{
	`{"type":"session","version":4,"id":"` + deepseekTestSessionID + `","createdAt":1792333057101,"cwd":"/repo","isSeeded":false,"delegationDepth":0}` + "\n" +
		`{"type":"request/context","seq":13,"time":1792333057468,"data":{"provider":"deepseek-official","model":"deepseek-v4-flash","contextWindow":1000000}}` + "\n",
	`{"type":"assistant/message","seq":16,"time":1792333059993,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[],"source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"},"id":"6d93d1cf-f4f4-4141-b0c7-c6849c2f9e40"},"usage":{"inputTokens":15172,"outputTokens":137,"cacheReadTokens":384,"cacheWriteTokens":0,"totalTokens":15693},"stream":[]}}` + "\n" +
		`{"type":"turn/end","seq":18,"time":1792333060001,"data":{"turn":1,"reason":"completed"}}` + "\n",
	`{"type":"assistant/message","seq":29,"time":1792333071000,"data":{"turn":2,"step":1,"message":{"role":"assistant","content":[],"source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-pro"},"id":"0b1f6a52-3d0c-4f39-9d55-0c58b5e2a8f1"},"usage":{"inputTokens":15807,"outputTokens":90,"cacheReadTokens":0,"cacheWriteTokens":0,"totalTokens":15897},"stream":[]}}` + "\n" +
		`{"type":"assistant/message","seq":35,"time":1792333080000,"data":{"turn":3`,
}

func writeDeepSeekTestLog(t *testing.T, path string, frames []string) {
	t.Helper()
	encoder, err := zstd.NewWriter(nil)
	mustNoError(t, err)
	var log bytes.Buffer
	for _, frame := range frames {
		log.Write(encoder.EncodeAll([]byte(frame), nil))
	}
	mustNoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	mustNoError(t, os.WriteFile(path, log.Bytes(), 0o600))
}

func TestReadAndParseDeepSeekSessionLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "--repo--", deepseekTestSessionID, "session.v4.jsonl.zstd")
	writeDeepSeekTestLog(t, path, deepseekTestFrames)

	ctx := context.Background()
	records, err := readDeepSeekReplies(ctx, path, deepseekTestSessionID, 0, 10)
	mustNoError(t, err)
	if len(records) != 2 || records[0].Offset != 16 || records[1].Offset != 29 {
		t.Fatalf("records = %+v", records)
	}
	newer, err := readDeepSeekReplies(ctx, path, deepseekTestSessionID, 16, 10)
	mustNoError(t, err)
	if len(newer) != 1 || newer[0].Offset != 29 {
		t.Fatalf("records after seq 16 = %+v", newer)
	}
	if _, err := readDeepSeekReplies(ctx, path, "another-session", 0, 10); err == nil {
		t.Fatal("a session log naming another session was read")
	}

	source := usageSource(domain.UsageSourceDeepSeekSession)
	source.NativeRootID = deepseekTestSessionID
	result := parseRecords(source, records, records[1].Offset, time.Now())
	if result.err != nil || len(result.Events) != 2 || result.Cursor.AnomalyCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	flash, pro := result.Events[0], result.Events[1]
	if flash.ProviderID != domain.UsageProviderOpenAI || flash.BillingProviderID != "deepseek" ||
		flash.ModelID != "deepseek-v4-flash" || tokenValue(flash.Tokens.InputTokens) != 15556 ||
		tokenValue(flash.Tokens.CachedInputTokens) != 384 || tokenValue(flash.Tokens.UncachedInputTokens) != 15172 ||
		tokenValue(flash.Tokens.OutputTokens) != 137 || flash.Costs.EstimatedCostNanos != nil ||
		!flash.CreatedAt.Equal(time.UnixMilli(1792333059993).UTC()) {
		t.Fatalf("flash event = %+v", flash)
	}
	if !strings.Contains(flash.ProviderUsageJSON, `"ao_derived_cache_write_input_tokens":0`) {
		t.Fatalf("provider usage %s lacks the cache-write count the pricer reads", flash.ProviderUsageJSON)
	}
	if pro.ModelID != "deepseek-v4-pro" || tokenValue(pro.Tokens.InputTokens) != 15807 ||
		tokenValue(pro.Tokens.OutputTokens) != 90 || flash.SourceEventKey == pro.SourceEventKey {
		t.Fatalf("pro event = %+v", pro)
	}
}

func newDeepSeekTestIngestion(t *testing.T, clock *time.Time) (store *sqlite.Store, session domain.SessionRecord, source domain.UsageSourceRecord) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "--repo--", deepseekTestSessionID, "session.v4.jsonl.zstd")
	writeDeepSeekTestLog(t, path, deepseekTestFrames)
	store, session = seedUsageTestSession(
		t, t.TempDir(), "usage", domain.HarnessDeepSeek, domain.ActivityActive, deepseekTestSessionID, *clock,
	)
	binding := seedUsageTestBinding(t, store, session, deepseekTestSessionID, domain.UsageBindingActive, *clock)
	identity, err := usagesvc.SourceIdentity(context.Background(), path)
	mustNoError(t, err)
	source, err = store.InsertUsageSource(context.Background(), domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceDeepSeekSession,
		NativeSessionID: deepseekTestSessionID,
		ArtifactPath:    path,
		FileIdentity:    identity,
		State:           domain.UsageSourcePending,
		UpdatedAt:       *clock,
	})
	mustNoError(t, err)
	return store, session, source
}

type failFirstApplyStore struct {
	ingestorStore
	failed bool
}

func (s *failFirstApplyStore) ApplyUsageChunk(
	ctx context.Context, sourceID, offset int64, updatedAt time.Time,
	cursor domain.SourceCursorState, events []domain.ModelUsageEvent,
) error {
	if !s.failed {
		s.failed = true
		return errors.New("writer busy")
	}
	return s.ingestorStore.ApplyUsageChunk(ctx, sourceID, offset, updatedAt, cursor, events)
}

func TestIngestorRereadsDeepSeekLogAfterFailedApply(t *testing.T) {
	ctx := context.Background()
	clock := time.Unix(1700000000, 0).UTC()
	store, session, source := newDeepSeekTestIngestion(t, &clock)
	ingestor := NewIngestor(&failFirstApplyStore{ingestorStore: store}, IngestorConfig{Clock: func() time.Time { return clock }})

	if _, err := ingestor.Ingest(ctx, source.ID); err == nil {
		t.Fatal("first ingest succeeded, want the injected apply failure")
	}
	clock = clock.Add(time.Minute)
	_, err := ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	assertTokenAggregate(t, store, session.ID, 15556+137+15807+90)
}
