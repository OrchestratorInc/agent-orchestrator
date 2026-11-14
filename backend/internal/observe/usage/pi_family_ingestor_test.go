package usage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

func ompTitleSlot(t *testing.T, title string) string {
	t.Helper()
	const width = 255
	line := `{"type":"title","v":1,"title":"` + title + `","source":"auto","updatedAt":"2026-08-28T14:17:13.741Z","pad":""}`
	if len(line) > width {
		t.Fatalf("title slot %q is wider than %d bytes", title, width)
	}
	return strings.Replace(line, `"pad":""`, `"pad":"`+strings.Repeat(" ", width-len(line))+`"`, 1) + "\n"
}

func TestIngestorDedupesOMPTranscriptAfterTitleSlotRewrite(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	now := time.Unix(1700000000, 0).UTC()
	store, session := seedUsageTestSession(
		t, dataDir, "usage", domain.HarnessOMP, domain.ActivityIdle, "", now,
	)
	const nativeID = "01a048bb-551d-7000-8b7e-1a07961198e0"
	binding, err := store.UpsertUsageBinding(ctx, domain.UsageBindingRecord{
		SessionID:    session.ID,
		Harness:      domain.HarnessOMP,
		NativeRootID: nativeID,
		State:        domain.UsageBindingActive,
		UpdatedAt:    now,
	})
	mustNoError(t, err)
	body := ompSessionRecord + "\n" + ompZaiCachedReply + "\n" + ompFailedReply + "\n"
	path := filepath.Join(t.TempDir(), "2026-08-28T14-17-11-709Z_"+nativeID+".jsonl")
	mustNoError(t, os.WriteFile(path, []byte(ompTitleSlot(t, "New session")+body), 0o600))
	path = canonicalTranscriptPath(path)
	identity, err := usagesvc.SourceIdentity(ctx, path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceOMPSession,
		NativeSessionID: nativeID,
		ArtifactPath:    path,
		FileIdentity:    identity,
		State:           domain.UsageSourcePending,
		UpdatedAt:       now,
	})
	mustNoError(t, err)

	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})
	ingestSourceFully(ctx, t, ingestor, source.ID)
	assertTokenAggregate(t, store, session.ID, 41368+426)

	mustNoError(t, os.WriteFile(path, []byte(ompTitleSlot(t, "Fix the flaky watcher test")+body+ompJudgeCall+"\n"), 0o600))
	result, err := ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	if result.ReplacementSourceID == 0 {
		t.Fatalf("title rewrite result = %+v, want a replacement generation", result)
	}
	ingestSourceFully(ctx, t, ingestor, result.ReplacementSourceID)
	assertTokenAggregate(t, store, session.ID, 41368+426+812+9)

	db := openUsageRawDB(t, dataDir)
	rows, err := db.Query(`SELECT pricing_version, estimated_cost_nanos FROM model_usage_events
WHERE binding_id = ? ORDER BY estimated_cost_nanos DESC`, binding.ID)
	mustNoError(t, err)
	defer func() { _ = rows.Close() }()
	var got []int64
	for rows.Next() {
		var version string
		var total int64
		mustNoError(t, rows.Scan(&version, &total))
		if version != ompPricingVersion {
			t.Fatalf("pricing version = %q, want %q", version, ompPricingVersion)
		}
		got = append(got, total)
	}
	mustNoError(t, rows.Err())
	if len(got) != 2 || got[0] != 13_168_160 || got[1] != 1_176_400 {
		t.Fatalf("OMP costs = %v, want OMP's 13168160 and 1176400 nanos once each", got)
	}
}
