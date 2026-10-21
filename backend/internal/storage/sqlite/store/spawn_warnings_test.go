package store_test

import (
	"context"
	"testing"
	"time"
)

func TestSetSessionSpawnWarningsRoundTripsAndSurvivesFullRowUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.SpawnWarnings) != 0 {
		t.Fatalf("new session warnings = %q, want none", r.SpawnWarnings)
	}

	warnings := []string{"Could not fetch origin/main for mer before spawning, so the worktree may start from a stale local base."}
	if err := s.SetSessionSpawnWarnings(ctx, r.ID, warnings, r.UpdatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.GetSession(ctx, r.ID)
	if err != nil || !found {
		t.Fatalf("get session: found=%v err=%v", found, err)
	}
	if len(got.SpawnWarnings) != 1 || got.SpawnWarnings[0] != warnings[0] {
		t.Fatalf("warnings did not round-trip: %q", got.SpawnWarnings)
	}

	// A later full-row update is what the controller and lifecycle write. It
	// must not erase the warnings, which only the dedicated setter writes.
	got.DisplayName = "renamed after spawn"
	if err := s.UpdateSession(ctx, got); err != nil {
		t.Fatal(err)
	}
	after, found, err := s.GetSession(ctx, r.ID)
	if err != nil || !found {
		t.Fatalf("get session after update: found=%v err=%v", found, err)
	}
	if len(after.SpawnWarnings) != 1 || after.SpawnWarnings[0] != warnings[0] {
		t.Fatalf("full-row update erased warnings: %q", after.SpawnWarnings)
	}
}

func TestSetSessionSpawnWarningsDoesNotEmitSessionEvent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	base, _ := s.LatestSeq(ctx)
	if err := s.SetSessionSpawnWarnings(ctx, r.ID, []string{"stale base"}, r.UpdatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// The migration deliberately leaves the session change trigger alone, so
	// recording a warning does not wake open sessions. Lock that in.
	if evs, err := s.EventsAfter(ctx, base, 100); err != nil || len(evs) != 0 {
		t.Fatalf("warning events = %+v err=%v, want none", evs, err)
	}
}
