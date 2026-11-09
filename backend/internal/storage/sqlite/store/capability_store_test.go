package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// The production store must be usable directly as the recorder's backing store;
// a mismatch here would only show up at daemon wiring time otherwise.
var _ capability.Store = (*sqlite.Store)(nil)

// Durable harness capability evidence.
//
// The store neither grades nor filters: it persists the snapshot a recorder
// observed and returns it unchanged, because a storage layer that silently
// upgraded evidence would make every gate downstream trust a grade nobody
// observed. These tests prove the round trip is lossless in both scopes.

func sampleCapabilitySnapshot(
	harness domain.AgentHarness,
	at time.Time,
) domain.HarnessCapabilitySnapshot {
	snapshot := domain.NewHarnessCapabilitySnapshot(
		harness, domain.CapabilitySourceConfirmedRuntime, at, 2*time.Minute)
	snapshot.AuthState = domain.AgentAuthenticationAuthorized
	snapshot.Readiness = domain.CapabilityReady
	snapshot.Surfaces = domain.CapabilitySurfaceChat
	snapshot.Streaming = domain.CapabilitySupported
	snapshot.Steer = domain.CapabilitySupported
	snapshot.Interrupt = domain.CapabilityUnsupported
	snapshot.Resume = domain.CapabilityResumeWithHistory
	snapshot.Approval = domain.CapabilityApprovalEnforced
	snapshot.ApplyAt = domain.CapabilityApplyAtTurn
	snapshot.Usage = domain.CapabilityUsageEstimated
	snapshot.Models = []domain.CapabilityModelChoice{
		{Model: "gpt-5-codex", Effort: []string{"low", "high"}, Default: "high"},
	}
	snapshot.Executable = "/usr/local/bin/codex"
	snapshot.Version = "0.146.0"
	snapshot.ReasonCode = "supported"
	return snapshot
}

func TestHarnessCapabilitySnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	observedAt := time.Now().UTC().Truncate(time.Second)
	want := sampleCapabilitySnapshot(domain.HarnessCodex, observedAt)

	// Absent evidence is not an empty answer.
	if _, found, err := s.GetHarnessCapabilitySnapshot(ctx, domain.HarnessCodex); err != nil || found {
		t.Fatalf("load before record: found=%v err=%v", found, err)
	}

	if err := s.UpsertHarnessCapabilitySnapshot(ctx, want); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, found, err := s.GetHarnessCapabilitySnapshot(ctx, domain.HarnessCodex)
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if got.Source != want.Source || got.Harness != want.Harness {
		t.Errorf("identity = %q/%q, want %q/%q", got.Harness, got.Source, want.Harness, want.Source)
	}
	if got.Steer != want.Steer || got.Interrupt != want.Interrupt || got.Streaming != want.Streaming {
		t.Errorf("features = steer %q interrupt %q streaming %q, want %q/%q/%q",
			got.Steer, got.Interrupt, got.Streaming, want.Steer, want.Interrupt, want.Streaming)
	}
	if got.Resume != want.Resume || got.Approval != want.Approval ||
		got.ApplyAt != want.ApplyAt || got.Usage != want.Usage {
		t.Errorf("support fields = %q/%q/%q/%q, want %q/%q/%q/%q",
			got.Resume, got.Approval, got.ApplyAt, got.Usage,
			want.Resume, want.Approval, want.ApplyAt, want.Usage)
	}
	if got.Executable != want.Executable || got.Version != want.Version {
		t.Errorf("metadata = %q/%q, want %q/%q",
			got.Executable, got.Version, want.Executable, want.Version)
	}
	if len(got.Models) != 1 || got.Models[0].Model != "gpt-5-codex" ||
		got.Models[0].Default != "high" || len(got.Models[0].Effort) != 2 {
		t.Errorf("models = %+v, want the advertised catalog", got.Models)
	}
	if !got.ObservedAt.Equal(want.ObservedAt) || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("times = %v..%v, want %v..%v",
			got.ObservedAt, got.ExpiresAt, want.ObservedAt, want.ExpiresAt)
	}

	// A later observation replaces the earlier one: keeping both would let stale
	// support outlive the provider that reported it.
	updated := want
	updated.Steer = domain.CapabilityUnsupported
	updated.ObservedAt = observedAt.Add(time.Minute)
	updated.ExpiresAt = updated.ObservedAt.Add(2 * time.Minute)
	if err := s.UpsertHarnessCapabilitySnapshot(ctx, updated); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _, err = s.GetHarnessCapabilitySnapshot(ctx, domain.HarnessCodex)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Steer != domain.CapabilityUnsupported {
		t.Errorf("steer after update = %q, want unsupported", got.Steer)
	}
}

func TestSessionCapabilitySnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	rec := sampleRecord("mer")
	rec.Harness, rec.Mode = domain.HarnessCodex, domain.SessionModeChat
	rec.Metadata.ControllerGeneration = "gen-1"
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	want := sampleCapabilitySnapshot(domain.HarnessCodex, time.Now().UTC().Truncate(time.Second))
	want.ObservedSessionID = session.ID
	want.Generation = "gen-1"
	if err := s.UpsertSessionCapabilitySnapshot(ctx, want); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, found, err := s.GetSessionCapabilitySnapshot(ctx, session.ID)
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if got.ObservedSessionID != session.ID || got.Generation != "gen-1" {
		t.Errorf("identity = %q/%q, want %q/gen-1", got.ObservedSessionID, got.Generation, session.ID)
	}
	if !got.AppliesTo(session.ID, "gen-1") {
		t.Error("recorded evidence does not apply to the generation it was observed in")
	}
	if got.AppliesTo(session.ID, "gen-2") {
		t.Error("recorded evidence claims to describe a rotated generation")
	}
	if !got.Steer.Supported() || got.Interrupt != domain.CapabilityUnsupported {
		t.Errorf("features = steer %q interrupt %q, want supported/unsupported",
			got.Steer, got.Interrupt)
	}
}

// The two scopes are separate rows, so a harness observation cannot shadow a
// session one.
func TestCapabilityScopesDoNotCollide(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	rec := sampleRecord("mer")
	rec.Harness, rec.Mode = domain.HarnessCodex, domain.SessionModeChat
	rec.Metadata.ControllerGeneration = "gen-1"
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	harnessScoped := sampleCapabilitySnapshot(domain.HarnessCodex, time.Now().UTC().Truncate(time.Second))
	harnessScoped.Source = domain.CapabilitySourceProviderHandshake
	harnessScoped.ReasonCode = "handshake"
	if err := s.UpsertHarnessCapabilitySnapshot(ctx, harnessScoped); err != nil {
		t.Fatalf("record harness snapshot: %v", err)
	}
	sessionScoped := harnessScoped
	sessionScoped.ObservedSessionID = session.ID
	sessionScoped.Generation = "gen-1"
	sessionScoped.Source = domain.CapabilitySourceConfirmedRuntime
	if err := s.UpsertSessionCapabilitySnapshot(ctx, sessionScoped); err != nil {
		t.Fatalf("record session snapshot: %v", err)
	}

	loadedHarness, _, err := s.GetHarnessCapabilitySnapshot(ctx, domain.HarnessCodex)
	if err != nil {
		t.Fatalf("load harness snapshot: %v", err)
	}
	if loadedHarness.SessionScoped() {
		t.Error("the harness row grew a session identity")
	}
	if loadedHarness.Source != domain.CapabilitySourceProviderHandshake {
		t.Errorf("harness source = %q, want provider_handshake", loadedHarness.Source)
	}
	loadedSession, _, err := s.GetSessionCapabilitySnapshot(ctx, session.ID)
	if err != nil {
		t.Fatalf("load session snapshot: %v", err)
	}
	if loadedSession.Source != domain.CapabilitySourceConfirmedRuntime {
		t.Errorf("session source = %q, want confirmed_runtime", loadedSession.Source)
	}
}

// A snapshot whose identity contradicts the table it is written to must be
// refused rather than stored under the wrong scope.
func TestCapabilitySnapshotScopeMustMatchTheTable(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	sessionScoped := sampleCapabilitySnapshot(domain.HarnessCodex, time.Now().UTC())
	sessionScoped.ObservedSessionID = "p1-1"
	sessionScoped.Generation = "gen-1"
	if err := s.UpsertHarnessCapabilitySnapshot(ctx, sessionScoped); err == nil {
		t.Fatal("a session-scoped snapshot was accepted by the harness table")
	}

	harnessScoped := sampleCapabilitySnapshot(domain.HarnessCodex, time.Now().UTC())
	if err := s.UpsertSessionCapabilitySnapshot(ctx, harnessScoped); err == nil {
		t.Fatal("a harness-scoped snapshot was accepted by the session table")
	}
}
