package usage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing/catalogsync"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestLegacyRepairerPricesDurablePrefixAndPreservesCursorState(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedCodexIngestionSource(t, dataDir)
	content := legacyCodexTranscript(true)
	mustNoError(t, os.WriteFile(path, []byte(content), 0o600))
	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})
	ingestSourceFully(ctx, t, ingestor, source.ID)
	before, ok, err := store.GetUsageSourceForIngestion(ctx, source.ID)
	mustNoError(t, err)
	if !ok || before.Source.ByteOffset != int64(len(content)) {
		t.Fatalf("durable source = %+v ok=%v", before.Source, ok)
	}
	makeLegacyProviderNull(t, dataDir, source.ID)

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	mustNoError(t, err)
	_, err = file.WriteString(string(codexTokenLine("suffix", 200, 100, 0, 40, 10)) + "\n")
	mustNoError(t, err)
	mustNoError(t, file.Close())

	snapshot := testPricingSnapshot(t, "0.000001")
	repairer := NewLegacyRepairer(store, pricing.NewManager(snapshot), LegacyRepairerConfig{
		Clock: func() time.Time { return now.Add(time.Hour) },
	})
	mustNoError(t, repairer.Run(ctx))

	assertLegacyRepair(t, dataDir, source.ID, "openai", domain.UsageBillingProviderObserved,
		86_000, snapshot.ProviderVersion("openai"))
	after, ok, err := store.GetUsageSourceForIngestion(ctx, source.ID)
	mustNoError(t, err)
	if !ok || after.Source.ByteOffset != before.Source.ByteOffset ||
		after.Source.ParserStateJSON != before.Source.ParserStateJSON {
		t.Fatalf("repair changed cursor/parser: before=%+v after=%+v", before.Source, after.Source)
	}
}

func TestLegacyRepairerRefusesUnverifiableSourcesAndMismatchedEvents(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, string, *sqlite.Store, int64)
	}{
		{
			name: "missing transcript",
			mutate: func(t *testing.T, _ string, path string, _ *sqlite.Store, _ int64) {
				mustNoError(t, os.Remove(path))
			},
		},
		{
			name: "replaced transcript identity",
			mutate: func(t *testing.T, _ string, path string, _ *sqlite.Store, _ int64) {
				before, err := os.Stat(path)
				mustNoError(t, err)
				replacement := path + ".replacement"
				mustNoError(t, os.WriteFile(replacement, []byte(legacyCodexTranscript(true)), 0o600))
				mustNoError(t, os.Rename(replacement, path))
				after, err := os.Stat(path)
				mustNoError(t, err)
				if os.SameFile(before, after) {
					t.Fatalf("replacement reused the file identity of %s", path)
				}
			},
		},
		{
			name: "checkpoint mismatch",
			mutate: func(t *testing.T, _ string, path string, _ *sqlite.Store, _ int64) {
				file, err := os.OpenFile(path, os.O_WRONLY, 0o600)
				mustNoError(t, err)
				_, err = file.WriteAt([]byte("X"), 0)
				mustNoError(t, err)
				mustNoError(t, file.Close())
			},
		},
		{
			name: "generic event mismatch",
			mutate: func(t *testing.T, dataDir string, _ string, _ *sqlite.Store, sourceID int64) {
				db := openUsageRawDB(t, dataDir)
				_, err := db.Exec(`UPDATE model_usage_events SET model_id = 'raced-model'
WHERE usage_source_id = ?`, sourceID)
				mustNoError(t, err)
			},
		},
		{
			name: "retired replacement source",
			mutate: func(t *testing.T, dataDir string, _ string, _ *sqlite.Store, sourceID int64) {
				db := openUsageRawDB(t, dataDir)
				_, err := db.Exec(`UPDATE usage_sources
SET state = 'complete', last_error_code = 'artifact_replaced' WHERE id = ?`, sourceID)
				mustNoError(t, err)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			dataDir := t.TempDir()
			store, source, path, now := seedCodexIngestionSource(t, dataDir)
			mustNoError(t, os.WriteFile(path, []byte(legacyCodexTranscript(true)), 0o600))
			ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{
				Clock: func() time.Time { return now },
			}), source.ID)
			makeLegacyProviderNull(t, dataDir, source.ID)
			tt.mutate(t, dataDir, path, store, source.ID)

			repairer := NewLegacyRepairer(store, pricing.NewManager(testPricingSnapshot(t, "0.000001")), LegacyRepairerConfig{})
			mustNoError(t, repairer.Run(ctx))
			assertLegacyStillNull(t, dataDir, source.ID)
		})
	}
}

func TestLegacyRepairerReplaysFinalizedNoNewlineRecord(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedCodexIngestionSource(t, dataDir)
	content := legacyCodexTranscript(false)
	mustNoError(t, os.WriteFile(path, []byte(content), 0o600))
	_, err := store.UpdateUsageBindingState(ctx, source.BindingID, domain.UsageBindingFinalizing, "", now)
	mustNoError(t, err)
	clock := now
	ingestor := NewIngestor(store, IngestorConfig{
		Clock: func() time.Time { return clock }, FinalizationWait: time.Second,
	})
	_, _ = ingestor.Ingest(ctx, source.ID)
	clock = clock.Add(2 * time.Second)
	_, err = ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	makeLegacyProviderNull(t, dataDir, source.ID)

	snapshot := testPricingSnapshot(t, "0.000001")
	mustNoError(t, NewLegacyRepairer(store, pricing.NewManager(snapshot), LegacyRepairerConfig{}).Run(ctx))
	assertLegacyRepair(t, dataDir, source.ID, "openai", domain.UsageBillingProviderObserved,
		86_000, snapshot.ProviderVersion("openai"))
}

func legacyCodexTranscript(finalNewline bool) string {
	content := `{"type":"session_meta","payload":{"model_provider":"openai"}}` + "\n" +
		`{"type":"turn_context","payload":{"model":"gpt-test"}}` + "\n" +
		string(codexTokenLine("durable", 100, 60, 0, 20, 5))
	if finalNewline {
		content += "\n"
	}
	return content
}

func openUsageRawDB(t *testing.T, dataDir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db"))
	mustNoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func makeLegacyProviderNull(t *testing.T, dataDir string, sourceID int64) {
	t.Helper()
	db := openUsageRawDB(t, dataDir)
	_, err := db.Exec(`UPDATE model_usage_events
SET billing_provider_id = NULL, provider_usage_json = NULL,
    input_cost_nanos = NULL, cached_input_cost_nanos = NULL,
    output_cost_nanos = NULL, estimated_cost_nanos = NULL,
    pricing_version = ''
WHERE usage_source_id = ?`, sourceID)
	mustNoError(t, err)
}

func assertLegacyRepair(
	t *testing.T, dataDir string, sourceID int64,
	provider string, source domain.UsageBillingProviderSource, total int64, version string,
) {
	t.Helper()
	db := openUsageRawDB(t, dataDir)
	var gotProvider, gotSource, gotVersion string
	var gotTotal int64
	mustNoError(t, db.QueryRow(`SELECT billing_provider_id, billing_provider_source,
estimated_cost_nanos, pricing_version
FROM model_usage_events WHERE usage_source_id = ?`, sourceID).Scan(
		&gotProvider, &gotSource, &gotTotal, &gotVersion))
	if gotProvider != provider || domain.UsageBillingProviderSource(gotSource) != source ||
		gotTotal != total || gotVersion != version {
		t.Fatalf("legacy repair = provider %q source %q total %d version %q",
			gotProvider, gotSource, gotTotal, gotVersion)
	}
}

func assertLegacyStillNull(t *testing.T, dataDir string, sourceID int64) {
	t.Helper()
	db := openUsageRawDB(t, dataDir)
	var provider sql.NullString
	mustNoError(t, db.QueryRow(`SELECT billing_provider_id FROM model_usage_events
WHERE usage_source_id = ?`, sourceID).Scan(&provider))
	if provider.Valid {
		t.Fatalf("legacy billing provider = %q, want NULL", provider.String)
	}
}

func TestLegacyReplayIndexRetainsOnlyCandidateEvents(t *testing.T) {
	candidates := []domain.LegacyUsageEvent{
		{SourceEventKey: "wanted", ModelID: "claude-x", MeasurementKind: domain.UsageMeasurementNativeReported},
		{SourceEventKey: "ambiguous", ModelID: "claude-x", MeasurementKind: domain.UsageMeasurementNativeReported},
	}
	index := newLegacyReplayIndex(candidates)
	for chunk := 0; chunk < 1000; chunk++ {
		index.observe([]domain.ModelUsageEvent{
			{SourceEventKey: fmt.Sprintf("unrelated-%d", chunk), ModelID: "noise"},
		})
	}
	index.observe([]domain.ModelUsageEvent{
		{SourceEventKey: "wanted", ModelID: "claude-x", MeasurementKind: domain.UsageMeasurementNativeReported},
	})
	index.observe([]domain.ModelUsageEvent{
		{SourceEventKey: "ambiguous", ModelID: "claude-x", MeasurementKind: domain.UsageMeasurementNativeReported},
	})
	index.observe([]domain.ModelUsageEvent{
		{SourceEventKey: "ambiguous", ModelID: "claude-x", MeasurementKind: domain.UsageMeasurementNativeReported},
	})

	if len(index.byKey) != 2 {
		t.Fatalf("retained %d events, want only the two candidate keys", len(index.byKey))
	}
	matches := matchLegacyEvents(candidates, index)
	if len(matches) != 1 || matches[0].candidate.SourceEventKey != "wanted" {
		t.Fatalf("matches = %+v, want only the unambiguous candidate", matches)
	}
}

func TestLegacyRepairerAttributesClaudeHistoryFromTheBindingRouteHint(t *testing.T) {
	tests := []struct {
		name         string
		providerHint string
		model        string
		wantSource   domain.UsageBillingProviderSource
	}{
		{name: "route hint present", providerHint: "anthropic", model: "claude-test",
			wantSource: domain.UsageBillingProviderObserved},
		{name: "no hint, model in catalog", model: "claude-test",
			wantSource: domain.UsageBillingProviderInferred},
		{name: "no hint, model unknown to every catalog", model: "glm-5.3"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			dataDir := t.TempDir()
			store, source, path, now := seedClaudeIngestionSource(t, dataDir, test.providerHint)
			mustNoError(t, os.WriteFile(path, []byte(legacyClaudeTranscript(test.model)), 0o600))
			ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{
				Clock: func() time.Time { return now },
			}), source.ID)
			makeLegacyProviderNull(t, dataDir, source.ID)

			snapshot := claudePricingSnapshot(t)
			mustNoError(t, NewLegacyRepairer(store, pricing.NewManager(snapshot), LegacyRepairerConfig{
				Clock: func() time.Time { return now.Add(time.Hour) },
			}).Run(ctx))

			if test.wantSource == "" {
				assertLegacyStillNull(t, dataDir, source.ID)
				return
			}
			assertLegacyRepair(t, dataDir, source.ID, "anthropic", test.wantSource,
				795_000, snapshot.ProviderVersion("anthropic"))
		})
	}
}

func TestLegacyRepairerLetsAnObservationCorrectAnInference(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedClaudeIngestionSource(t, dataDir, "")
	mustNoError(t, os.WriteFile(path, []byte(legacyClaudeTranscript("claude-test")), 0o600))
	snapshot := claudePricingSnapshot(t)
	ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{
		Clock:   func() time.Time { return now },
		Pricing: pricing.NewManager(snapshot),
	}), source.ID)
	assertLegacyStillNull(t, dataDir, source.ID)
	mustNoError(t, NewLegacyRepairer(store, pricing.NewManager(snapshot), LegacyRepairerConfig{
		Clock: func() time.Time { return now.Add(time.Minute) },
	}).Run(ctx))
	assertLegacyRepair(t, dataDir, source.ID, "anthropic", domain.UsageBillingProviderInferred,
		795_000, snapshot.ProviderVersion("anthropic"))

	db := openUsageRawDB(t, dataDir)
	_, err := db.Exec(
		`UPDATE usage_bindings SET provider_hint = 'zai' WHERE native_root_id = 'claude-root'`)
	mustNoError(t, err)

	mustNoError(t, NewLegacyRepairer(store, pricing.NewManager(snapshot), LegacyRepairerConfig{
		Clock: func() time.Time { return now.Add(time.Hour) },
	}).Run(ctx))

	var provider, providerSource string
	var total sql.NullInt64
	mustNoError(t, db.QueryRow(`SELECT billing_provider_id, billing_provider_source, estimated_cost_nanos
FROM model_usage_events WHERE usage_source_id = ?`, source.ID).Scan(&provider, &providerSource, &total))
	if provider != "zai" || providerSource != string(domain.UsageBillingProviderObserved) || total.Valid {
		t.Fatalf("corrected row = provider %q source %q total %v", provider, providerSource, total)
	}
}

func TestLegacyRepairerLeavesAnInferenceAloneWithoutNewEvidence(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedClaudeIngestionSource(t, dataDir, "")
	mustNoError(t, os.WriteFile(path, []byte(legacyClaudeTranscript("claude-test")), 0o600))
	snapshot := claudePricingSnapshot(t)
	ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{
		Clock:   func() time.Time { return now },
		Pricing: pricing.NewManager(snapshot),
	}), source.ID)
	mustNoError(t, NewLegacyRepairer(store, pricing.NewManager(snapshot), LegacyRepairerConfig{
		Clock: func() time.Time { return now.Add(time.Minute) },
	}).Run(ctx))

	db := openUsageRawDB(t, dataDir)
	_, err := db.Exec(`UPDATE model_usage_events
SET input_cost_nanos = NULL, cached_input_cost_nanos = NULL,
    output_cost_nanos = NULL, estimated_cost_nanos = NULL
WHERE usage_source_id = ?`, source.ID)
	mustNoError(t, err)

	mustNoError(t, NewLegacyRepairer(store, pricing.NewManager(snapshot), LegacyRepairerConfig{
		Clock: func() time.Time { return now.Add(time.Hour) },
	}).Run(ctx))

	var provider, providerSource string
	var total sql.NullInt64
	mustNoError(t, db.QueryRow(`SELECT billing_provider_id, billing_provider_source, estimated_cost_nanos
FROM model_usage_events WHERE usage_source_id = ?`, source.ID).Scan(&provider, &providerSource, &total))
	if provider != "anthropic" || providerSource != string(domain.UsageBillingProviderInferred) || total.Valid {
		t.Fatalf("untouched row = provider %q source %q total %v", provider, providerSource, total)
	}
}

func TestIngestorRequestsRepairWhenARouteLandsMidChunk(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedClaudeIngestionSource(t, dataDir, "")
	mustNoError(t, os.WriteFile(path, []byte(legacyClaudeTranscript("claude-test")), 0o600))

	requested := 0
	ingestor := NewIngestor(store, IngestorConfig{
		Clock:                    func() time.Time { return now },
		Pricing:                  pricing.NewManager(claudePricingSnapshot(t)),
		RequestAttributionRepair: func() { requested++ },
	})
	db := openUsageRawDB(t, dataDir)
	ingestor.afterRead = func() {
		_, err := db.Exec(
			`UPDATE usage_bindings SET provider_hint = 'anthropic' WHERE native_root_id = 'claude-root'`)
		mustNoError(t, err)
		ingestor.afterRead = nil
	}
	ingestSourceFully(ctx, t, ingestor, source.ID)

	assertLegacyStillNull(t, dataDir, source.ID)
	if requested == 0 {
		t.Fatal("no repair requested for rows the route-resolved pass could not have seen")
	}
}

func TestIngestorDoesNotRequestRepairWithoutARoute(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedClaudeIngestionSource(t, dataDir, "")
	mustNoError(t, os.WriteFile(path, []byte(legacyClaudeTranscript("claude-test")), 0o600))

	requested := 0
	ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{
		Clock:                    func() time.Time { return now },
		Pricing:                  pricing.NewManager(claudePricingSnapshot(t)),
		RequestAttributionRepair: func() { requested++ },
	}), source.ID)

	if requested != 0 {
		t.Fatalf("repair requested %d times for a binding with no route", requested)
	}
}

func TestLegacyRepairerRunsAgainAfterStartupIngestionSettles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dataDir := t.TempDir()
	store, source, path, now := seedClaudeIngestionSource(t, dataDir, "")
	snapshot := claudePricingSnapshot(t)

	ordered := &startupOrderingStore{
		Store:      store,
		firstPass:  make(chan struct{}),
		ingestDone: make(chan struct{}),
	}
	repairer := NewLegacyRepairer(ordered, pricing.NewManager(snapshot), LegacyRepairerConfig{
		Clock:         func() time.Time { return now.Add(time.Hour) },
		StartupSettle: time.Millisecond,
	})
	mustNoError(t, repairer.Start(ctx))
	<-ordered.firstPass

	mustNoError(t, os.WriteFile(path, []byte(legacyClaudeTranscript("claude-test")), 0o600))
	ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{
		Clock:   func() time.Time { return now },
		Pricing: pricing.NewManager(snapshot),
	}), source.ID)
	assertLegacyStillNull(t, dataDir, source.ID)
	close(ordered.ingestDone)

	db := openUsageRawDB(t, dataDir)
	deadline := time.Now().Add(10 * time.Second)
	for {
		var provider sql.NullString
		var total sql.NullInt64
		mustNoError(t, db.QueryRow(`SELECT billing_provider_id, estimated_cost_nanos
FROM model_usage_events WHERE usage_source_id = ?`, source.ID).Scan(&provider, &total))
		if provider.Valid && total.Valid {
			if provider.String != "anthropic" || total.Int64 != 795_000 {
				t.Fatalf("settled repair = provider %q total %d", provider.String, total.Int64)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("startup ingestion never got a repair pass without route evidence or a restart")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type startupOrderingStore struct {
	*sqlite.Store
	firstPass  chan struct{}
	ingestDone chan struct{}
	passes     atomic.Int64
}

func (s *startupOrderingStore) ListLegacyUsageSources(ctx context.Context) ([]domain.UsageSourceContext, error) {
	if s.passes.Add(1) == 1 {
		sources, err := s.Store.ListLegacyUsageSources(ctx)
		close(s.firstPass)
		return sources, err
	}
	select {
	case <-s.ingestDone:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.Store.ListLegacyUsageSources(ctx)
}

func TestLegacyRepairerDoesNotInferOverAnUnidentifiedRoute(t *testing.T) {
	for _, test := range []struct {
		name         string
		hint         string
		wantInferred bool
	}{
		{name: "no hook yet", hint: "", wantInferred: true},
		{name: "hook saw an unnamed route", hint: pricing.UnidentifiedBillingRoute},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			dataDir := t.TempDir()
			store, source, path, now := seedClaudeIngestionSource(t, dataDir, test.hint)
			mustNoError(t, os.WriteFile(path, []byte(legacyClaudeTranscript("claude-test")), 0o600))
			snapshot := claudePricingSnapshot(t)
			ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{
				Clock:   func() time.Time { return now },
				Pricing: pricing.NewManager(snapshot),
			}), source.ID)

			mustNoError(t, NewLegacyRepairer(store, pricing.NewManager(snapshot), LegacyRepairerConfig{
				Clock: func() time.Time { return now.Add(time.Hour) },
			}).Run(ctx))

			if !test.wantInferred {
				assertLegacyStillNull(t, dataDir, source.ID)
				return
			}
			assertLegacyRepair(t, dataDir, source.ID, "anthropic", domain.UsageBillingProviderInferred,
				795_000, snapshot.ProviderVersion("anthropic"))
		})
	}
}

func TestLegacyRepairerKeepsExistingInferenceWhenRouteIsUnidentified(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedClaudeIngestionSource(t, dataDir, "")
	mustNoError(t, os.WriteFile(path, []byte(legacyClaudeTranscript("claude-test")), 0o600))
	snapshot := claudePricingSnapshot(t)
	ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{
		Clock:   func() time.Time { return now },
		Pricing: pricing.NewManager(snapshot),
	}), source.ID)
	repairer := NewLegacyRepairer(store, pricing.NewManager(snapshot), LegacyRepairerConfig{
		Clock: func() time.Time { return now.Add(time.Hour) },
	})
	mustNoError(t, repairer.Run(ctx))
	assertLegacyRepair(t, dataDir, source.ID, "anthropic", domain.UsageBillingProviderInferred,
		795_000, snapshot.ProviderVersion("anthropic"))

	sessionID := sourceSessionID(t, store, source.ID)
	binding, ok, err := store.GetUsageBinding(ctx, sessionID, domain.HarnessClaudeCode, "claude-root")
	mustNoError(t, err)
	if !ok {
		t.Fatal("usage binding disappeared")
	}
	binding.ProviderHint = pricing.UnidentifiedBillingRoute
	binding.UpdatedAt = now.Add(2 * time.Hour)
	_, err = store.UpsertUsageBinding(ctx, binding)
	mustNoError(t, err)

	mustNoError(t, repairer.Run(ctx))
	assertLegacyRepair(t, dataDir, source.ID, "anthropic", domain.UsageBillingProviderInferred,
		795_000, snapshot.ProviderVersion("anthropic"))
}

func seedClaudeIngestionSource(
	t *testing.T, dataDir, providerHint string,
) (*sqlite.Store, domain.UsageSourceRecord, string, time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	store, session := seedUsageTestSession(
		t, dataDir, "usage", domain.HarnessClaudeCode, domain.ActivityIdle, "", now,
	)
	binding, err := store.UpsertUsageBinding(ctx, domain.UsageBindingRecord{
		SessionID:    session.ID,
		Harness:      domain.HarnessClaudeCode,
		NativeRootID: "claude-root",
		ProviderHint: providerHint,
		State:        domain.UsageBindingActive,
		UpdatedAt:    now,
	})
	mustNoError(t, err)
	path := canonicalTranscriptPath(filepath.Join(t.TempDir(), "transcript.jsonl"))
	mustNoError(t, os.WriteFile(path, nil, 0o600))
	identity, err := usagesvc.SourceIdentity(ctx, path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(ctx, domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceClaudeMain,
		NativeSessionID: "claude-root",
		ArtifactPath:    path,
		FileIdentity:    identity,
		State:           domain.UsageSourcePending,
		UpdatedAt:       now,
	})
	mustNoError(t, err)
	return store, source, path, now
}

func legacyClaudeTranscript(model string) string {
	return `{"type":"assistant","uuid":"one","timestamp":"2026-08-20T10:00:00Z","message":` +
		`{"id":"msg-1","model":"` + model + `","stop_reason":"end_turn","usage":` +
		`{"input_tokens":40,"cache_creation_input_tokens":20,"cache_read_input_tokens":500,"output_tokens":30,` +
		`"cache_creation":{"ephemeral_5m_input_tokens":20,"ephemeral_1h_input_tokens":0}}}}` + "\n"
}

func claudePricingSnapshot(t *testing.T) *pricing.Snapshot {
	t.Helper()
	upstream := []byte(`{
  "anthropic/claude-test": {
    "litellm_provider": "anthropic",
    "mode": "chat",
    "input_cost_per_token": 0.000003,
    "cache_read_input_token_cost": 0.0000003,
    "cache_creation_input_token_cost": 0.00000375,
    "output_cost_per_token": 0.000015
  },
  "openai/gpt-test": {"litellm_provider":"openai","mode":"chat","input_cost_per_token":0,"output_cost_per_token":0},
  "zai/glm-test": {"litellm_provider":"zai","mode":"chat","input_cost_per_token":0,"output_cost_per_token":0}
}`)
	catalog, err := catalogsync.Catalog(upstream)
	mustNoError(t, err)
	return catalog.Snapshot()
}

func TestLegacyRepairerRetriesAPassOutrunByIngestion(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedClaudeIngestionSource(t, dataDir, "anthropic")
	mustNoError(t, os.WriteFile(path, []byte(legacyClaudeTranscript("claude-test")), 0o600))
	ingestSourceFully(ctx, t, NewIngestor(store, IngestorConfig{
		Clock: func() time.Time { return now },
	}), source.ID)
	makeLegacyProviderNull(t, dataDir, source.ID)

	racing := &cursorRacingStore{Store: store, transcript: path, ingestor: NewIngestor(store, IngestorConfig{
		Clock: func() time.Time { return now },
	})}

	snapshot := claudePricingSnapshot(t)
	repairer := NewLegacyRepairer(racing, pricing.NewManager(snapshot), LegacyRepairerConfig{
		Clock:     func() time.Time { return now.Add(time.Hour) },
		RaceRetry: time.Millisecond,
	})
	raced, err := repairer.run(ctx)
	mustNoError(t, err)
	if !raced {
		t.Fatal("a repair dropped by the cursor guard was not reported as raced")
	}
	assertLegacyStillNull(t, dataDir, source.ID)

	raced, err = repairer.run(ctx)
	mustNoError(t, err)
	if raced {
		t.Fatal("settled source still reported a race")
	}
	assertLegacyRepair(t, dataDir, source.ID, "anthropic", domain.UsageBillingProviderObserved,
		795_000, snapshot.ProviderVersion("anthropic"))
}

type cursorRacingStore struct {
	*sqlite.Store
	transcript string
	ingestor   *Ingestor
	raceOnce   bool
}

func (s *cursorRacingStore) ApplyLegacyUsageRepairs(
	ctx context.Context, repairs []domain.LegacyUsageRepair, at time.Time,
) (int, error) {
	if !s.raceOnce {
		s.raceOnce = true
		file, err := os.OpenFile(s.transcript, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return 0, err
		}
		if _, err := file.WriteString(legacyClaudeTranscript("claude-test")); err != nil {
			return 0, err
		}
		if err := file.Close(); err != nil {
			return 0, err
		}
		if _, err := s.ingestor.Ingest(ctx, repairs[0].Candidate.UsageSourceID); err != nil {
			return 0, err
		}
	}
	return s.Store.ApplyLegacyUsageRepairs(ctx, repairs, at)
}
