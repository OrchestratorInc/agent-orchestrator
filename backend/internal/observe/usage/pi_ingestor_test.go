package usage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

func TestIngestorKeepsPiReportedCostAndPricesTheRest(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	now := time.Unix(1700000000, 0).UTC()
	store, session := seedUsageTestSession(
		t, dataDir, "usage", domain.HarnessPi, domain.ActivityIdle, "", now,
	)
	binding, err := store.UpsertUsageBinding(ctx, domain.UsageBindingRecord{
		SessionID:    session.ID,
		Harness:      domain.HarnessPi,
		NativeRootID: "pi-root",
		State:        domain.UsageBindingActive,
		UpdatedAt:    now,
	})
	mustNoError(t, err)
	path := filepath.Join(t.TempDir(), "pi-session.jsonl")
	mustNoError(t, os.WriteFile(path, []byte(
		`{"type":"session","version":3,"id":"pi-root","cwd":"/repo"}`+"\n"+
			`{"type":"message","id":"openai-1","timestamp":"2026-11-04T02:55:30Z","message":{"role":"assistant","provider":"openai","model":"gpt-test","usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0}}}`+"\n"+
			`{"type":"message","id":"zai-1","timestamp":"2026-11-04T02:58:59Z","message":{"role":"assistant","provider":"zai-coding-cn","model":"glm-5.3-flash","usage":{"input":4217,"output":99,"cacheRead":0,"cacheWrite":0,"cost":{"input":0.00063255,"output":0.0000495,"cacheRead":0,"cacheWrite":0,"total":0.00068205}}}}`+"\n",
	), 0o600))
	path = canonicalTranscriptPath(path)
	identity, err := usagesvc.SourceIdentity(ctx, path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourcePiSession,
		NativeSessionID: "pi-root",
		ArtifactPath:    path,
		FileIdentity:    identity,
		State:           domain.UsageSourcePending,
		UpdatedAt:       now,
	})
	mustNoError(t, err)

	snapshot := testPricingSnapshot(t, "0.000001")
	ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{
		Clock:   func() time.Time { return now },
		Pricing: pricing.NewManager(snapshot),
	}), source.ID)

	db := openUsageRawDB(t, dataDir)
	costs := map[string]struct {
		total   sql.NullInt64
		version string
	}{}
	rows, err := db.Query(`SELECT billing_provider_id, estimated_cost_nanos, pricing_version
FROM model_usage_events WHERE usage_source_id = ?`, source.ID)
	mustNoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var provider, version string
		var total sql.NullInt64
		mustNoError(t, rows.Scan(&provider, &total, &version))
		costs[provider] = struct {
			total   sql.NullInt64
			version string
		}{total, version}
	}
	mustNoError(t, rows.Err())

	if got := costs["openai"]; !got.total.Valid || got.total.Int64 != 14_000 ||
		got.version != snapshot.ProviderVersion("openai") {
		t.Fatalf("openai cost = %+v, want the catalog's 14000 nanos", got)
	}
	if got := costs["zai-coding-cn"]; !got.total.Valid || got.total.Int64 != 682_050 ||
		got.version != piPricingVersion {
		t.Fatalf("z.ai cost = %+v, want Pi's 682050 nanos", got)
	}
}

func TestIngestorCountsPiForkCopiesOnce(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	now := time.Unix(1700000000, 0).UTC()
	store, session := seedUsageTestSession(
		t, dataDir, "usage", domain.HarnessPi, domain.ActivityIdle, "", now,
	)
	copied := `{"type":"message","id":"a1","timestamp":"2026-11-04T02:55:30Z","message":{"role":"assistant","provider":"openai","model":"gpt-test","responseId":"resp_parent_1","usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0}}}` + "\n" +
		`{"type":"compaction","id":"c1","timestamp":"2026-11-04T02:56:00Z","summary":"s","usage":{"input":30,"output":4,"cacheRead":0,"cacheWrite":0}}` + "\n" +
		`{"type":"message","id":"a2","timestamp":"2026-11-04T02:57:00Z","message":{"role":"assistant","provider":"openai","model":"gpt-test","responseId":"resp_parent_2","usage":{"input":20,"output":3,"cacheRead":0,"cacheWrite":0}}}` + "\n"
	files := map[string]string{
		"pi-root": `{"type":"session","version":3,"id":"pi-root","cwd":"/repo"}` + "\n" + copied,
		"pi-fork": `{"type":"session","version":3,"id":"pi-fork","cwd":"/repo","parentSession":"pi-root"}` + "\n" + copied +
			`{"type":"message","id":"a3","timestamp":"2026-11-04T03:00:00Z","message":{"role":"assistant","provider":"openai","model":"gpt-test","responseId":"resp_fork_1","usage":{"input":40,"output":5,"cacheRead":0,"cacheWrite":0}}}` + "\n",
	}
	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})
	for _, root := range []string{"pi-root", "pi-fork"} {
		binding, err := store.UpsertUsageBinding(ctx, domain.UsageBindingRecord{
			SessionID:    session.ID,
			Harness:      domain.HarnessPi,
			NativeRootID: root,
			State:        domain.UsageBindingActive,
			UpdatedAt:    now,
		})
		mustNoError(t, err)
		path := filepath.Join(t.TempDir(), root+".jsonl")
		mustNoError(t, os.WriteFile(path, []byte(files[root]), 0o600))
		path = canonicalTranscriptPath(path)
		identity, err := usagesvc.SourceIdentity(ctx, path)
		mustNoError(t, err)
		source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
			BindingID:       binding.ID,
			Kind:            domain.UsageSourcePiSession,
			NativeSessionID: root,
			ArtifactPath:    path,
			FileIdentity:    identity,
			State:           domain.UsageSourcePending,
			UpdatedAt:       now,
		})
		mustNoError(t, err)
		ingestSourceFully(ctx, t, ingestor, source.ID)
	}
	assertTokenAggregate(t, store, session.ID, 114)
}
