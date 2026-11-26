package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestStorageSettingsSeededWithDefaultsAndRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetStorageSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	want := domain.DefaultStorageSettings()
	if got != want {
		t.Fatalf("seeded settings = %+v, want defaults %+v", got, want)
	}

	changed := want
	changed.AutoClean = false
	changed.OffloadIdleHours = 72
	changed.EndedWorktreeDays = 0
	changed.CleanDevProfiles = true
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	if err := s.SaveStorageSettings(ctx, changed, now); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	got, err = s.GetStorageSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	changed.UpdatedAt = now
	if got != changed {
		t.Fatalf("settings = %+v, want %+v", got, changed)
	}
}

func TestSessionStorageFactsOffloadThenRestore(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "st")
	rec, err := s.CreateSession(ctx, sampleRecord("st"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, ok, err := s.GetSessionStorageFacts(ctx, rec.ID); err != nil || ok {
		t.Fatalf("absent facts ok=%v err=%v", ok, err)
	}

	offloadedAt := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	if err := s.RecordSessionOffload(ctx, domain.SessionStorageFacts{
		SessionID:      rec.ID,
		OffloadedAt:    offloadedAt,
		OffloadedBytes: 1 << 30,
		OffloadedPaths: []string{"frontend/node_modules", "dist"},
		TemplateKeys:   map[string]string{"frontend/node_modules": "abc"},
	}); err != nil {
		t.Fatalf("record offload: %v", err)
	}
	facts, ok, err := s.GetSessionStorageFacts(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("get facts ok=%v err=%v", ok, err)
	}
	if !facts.Offloaded() || facts.OffloadedBytes != 1<<30 || len(facts.OffloadedPaths) != 2 || facts.TemplateKeys["frontend/node_modules"] != "abc" {
		t.Fatalf("facts = %+v", facts)
	}

	// A failed restore keeps the session offloaded and records why.
	if err := s.RecordSessionRestore(ctx, rec.ID, offloadedAt.Add(time.Hour), "setup failed"); err != nil {
		t.Fatalf("record failed restore: %v", err)
	}
	facts, _, _ = s.GetSessionStorageFacts(ctx, rec.ID)
	if !facts.Offloaded() || facts.RestoreError != "setup failed" {
		t.Fatalf("after failed restore facts = %+v", facts)
	}

	if err := s.RecordSessionRestore(ctx, rec.ID, offloadedAt.Add(2*time.Hour), ""); err != nil {
		t.Fatalf("record restore: %v", err)
	}
	all, err := s.ListSessionStorageFacts(ctx)
	if err != nil {
		t.Fatalf("list facts: %v", err)
	}
	if got := all[rec.ID]; got.Offloaded() || got.RestoreError != "" {
		t.Fatalf("after restore facts = %+v, want settled", got)
	}
}

func TestStorageActionsNewestFirstAndPruned(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	old := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	if err := s.InsertStorageActions(ctx, []domain.StorageAction{
		{At: old, Kind: domain.StorageCleanLogs, Target: "/a", Result: domain.StorageActionDone, Bytes: 5},
		{At: recent, Kind: domain.StorageCleanOffload, Target: "/b", SessionID: "st-1", Reason: "quiet", Result: domain.StorageActionSkipped, Error: "agent running"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	actions, err := s.ListStorageActions(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(actions) != 2 || actions[0].Target != "/b" || actions[0].SessionID != "st-1" || actions[0].Error != "agent running" || actions[1].Bytes != 5 {
		t.Fatalf("actions = %+v", actions)
	}
	removed, err := s.PruneStorageActionsBefore(ctx, recent.Add(-30*24*time.Hour))
	if err != nil || removed != 1 {
		t.Fatalf("prune removed=%d err=%v", removed, err)
	}
	actions, _ = s.ListStorageActions(ctx, 10)
	if len(actions) != 1 || actions[0].Target != "/b" {
		t.Fatalf("after prune actions = %+v", actions)
	}
}

func TestListSessionEndTimesOnlyTerminated(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "en")
	live, err := s.CreateSession(ctx, sampleRecord("en"))
	if err != nil {
		t.Fatalf("create live: %v", err)
	}
	endedRec := sampleRecord("en")
	endedRec.IsTerminated = true
	ended, err := s.CreateSession(ctx, endedRec)
	if err != nil {
		t.Fatalf("create ended: %v", err)
	}
	times, err := s.ListSessionEndTimes(ctx)
	if err != nil {
		t.Fatalf("list end times: %v", err)
	}
	if _, ok := times[live.ID]; ok {
		t.Fatalf("live session %s listed as ended", live.ID)
	}
	if got, ok := times[ended.ID]; !ok || got.IsZero() {
		t.Fatalf("ended session %s end time = %v ok=%v", ended.ID, got, ok)
	}
}

func TestDatabasePageStats(t *testing.T) {
	s := newTestStore(t)
	pageSize, pageCount, free, err := s.DatabasePageStats(context.Background())
	if err != nil {
		t.Fatalf("page stats: %v", err)
	}
	if pageSize <= 0 || pageCount <= 0 || free < 0 {
		t.Fatalf("pageSize=%d pageCount=%d free=%d", pageSize, pageCount, free)
	}
	if _, _, err := s.DatabaseTableSizes(context.Background(), 5); err != nil {
		t.Fatalf("table sizes: %v", err)
	}
}
