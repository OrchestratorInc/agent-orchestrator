package usage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

func TestLegacyRepairerFillsEventTimesFromACompressedRollout(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedCodexIngestionSource(t, dataDir)
	content := `{"type":"session_meta","payload":{"model_provider":"openai"}}` + "\n" +
		`{"type":"turn_context","payload":{"model":"gpt-test"}}` + "\n" +
		string(codexTokenLine("2026-10-01T10:00:00Z", 100, 60, 0, 20, 5)) + "\n" +
		string(codexTokenLine("2026-10-01T11:30:00Z", 300, 160, 0, 50, 5)) + "\n"
	mustNoError(t, os.WriteFile(path, []byte(content), 0o600))
	ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }}), source.ID)

	db := openUsageRawDB(t, dataDir)
	_, err := db.Exec(`UPDATE model_usage_events SET created_at = NULL WHERE usage_source_id = ?`, source.ID)
	mustNoError(t, err)
	encoder, err := zstd.NewWriter(nil)
	mustNoError(t, err)
	mustNoError(t, os.WriteFile(path+".zst", encoder.EncodeAll([]byte(content), nil), 0o600))
	mustNoError(t, os.Remove(path))

	runCtx, cancel := context.WithCancel(ctx)
	repairer := NewLegacyRepairer(store, pricing.NewManager(testPricingSnapshot(t, "0.000001")), LegacyRepairerConfig{})
	mustNoError(t, repairer.Start(runCtx))
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var untimed int
		mustNoError(t, db.QueryRow(`SELECT count(*) FROM model_usage_events WHERE created_at IS NULL`).Scan(&untimed))
		if untimed == 0 {
			break
		}
	}
	cancel()
	repairer.Wait()

	rows, err := db.Query(`SELECT created_at FROM model_usage_events WHERE usage_source_id = ? ORDER BY id`, source.ID)
	mustNoError(t, err)
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var at sql.NullTime
		mustNoError(t, rows.Scan(&at))
		got = append(got, at.Time.UTC().Format(time.RFC3339))
	}
	mustNoError(t, rows.Err())
	if len(got) != 2 || got[0] != "2026-10-01T10:00:00Z" || got[1] != "2026-10-01T11:30:00Z" {
		t.Fatalf("created_at = %v, want both record times", got)
	}
}

func TestLegacyRepairerFillsServiceTiersOnceFromTheTranscript(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedCodexIngestionSource(t, dataDir)
	content := `{"type":"session_meta","payload":{"model_provider":"openai"}}` + "\n" +
		`{"type":"turn_context","payload":{"model":"gpt-test"}}` + "\n" +
		string(codexTokenLine("2026-10-01T10:00:00Z", 100, 60, 0, 20, 5)) + "\n" +
		`{"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"gpt-test","service_tier":"priority"}}}` + "\n" +
		string(codexTokenLine("2026-10-01T11:30:00Z", 300, 160, 0, 50, 5)) + "\n"
	mustNoError(t, os.WriteFile(path, []byte(content), 0o600))
	ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }}), source.ID)

	db := openUsageRawDB(t, dataDir)
	_, err := db.Exec(`UPDATE model_usage_events SET service_tier = NULL, estimated_cost_nanos = 7, pricing_version = 'ao-catalog:openai:old' WHERE usage_source_id = ?`, source.ID)
	mustNoError(t, err)
	_, err = db.Exec(`INSERT INTO usage_tier_repairs (usage_source_id) VALUES (?)`, source.ID)
	mustNoError(t, err)

	filled := make(chan struct{}, 4)
	runCtx, cancel := context.WithCancel(ctx)
	repairer := NewLegacyRepairer(store, pricing.NewManager(testPricingSnapshot(t, "0.000001")), LegacyRepairerConfig{
		OnTiersFilled: func(context.Context) { filled <- struct{}{} },
	})
	mustNoError(t, repairer.Start(runCtx))
	select {
	case <-filled:
	case <-time.After(5 * time.Second):
		t.Fatal("tier repair never reported filled rows")
	}
	cancel()
	repairer.Wait()

	var queued int
	mustNoError(t, db.QueryRow(`SELECT count(*) FROM usage_tier_repairs`).Scan(&queued))
	if queued != 0 {
		t.Fatalf("queued sources = %d, want the source taken off the queue", queued)
	}
	rows, err := db.Query(`SELECT coalesce(service_tier, ''), coalesce(estimated_cost_nanos, -1), pricing_version FROM model_usage_events WHERE usage_source_id = ? ORDER BY id`, source.ID)
	mustNoError(t, err)
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var tier, version string
		var cost int64
		mustNoError(t, rows.Scan(&tier, &cost, &version))
		got = append(got, fmt.Sprintf("%s/%d/%s", tier, cost, version))
	}
	mustNoError(t, rows.Err())
	want := []string{"/7/ao-catalog:openai:old", "priority/-1/"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}
