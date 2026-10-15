package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLaunchesRecordTheManagedReleaseTheyRun(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	now := time.Now().UTC()
	inUse := func() []string {
		t.Helper()
		got, err := s.ListHarnessReleasesInUse(ctx, domain.HarnessCodex)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(got)
		return got
	}

	// A launch before AO manages the harness runs nothing of AO's.
	rec := sampleRecord("mer")
	rec.Harness, rec.Mode, rec.Metadata.ControllerGeneration = domain.HarnessCodex, domain.SessionModeChat, "gen-0"
	chat, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if got := inUse(); len(got) != 0 {
		t.Fatalf("in use before any managed release = %v", got)
	}

	if err := s.SetManagedHarnessRelease(ctx, domain.HarnessCodex, "v1", now); err != nil {
		t.Fatal(err)
	}
	// Its next launch (a new controller generation) records v1.
	current, _, _ := s.GetSession(ctx, chat.ID)
	current.Metadata.ControllerGeneration = "gen-1"
	if err := s.UpdateSession(ctx, current); err != nil {
		t.Fatal(err)
	}
	// A terminal spawned now records v1 too.
	tuiRec := sampleRecord("mer")
	tuiRec.Harness, tuiRec.Mode, tuiRec.Metadata.RuntimeLaunchID = domain.HarnessCodex, domain.SessionModeTUI, "launch-1"
	if _, err := s.CreateSession(ctx, tuiRec); err != nil {
		t.Fatal(err)
	}
	if got := inUse(); !slices.Equal(got, []string{"v1"}) {
		t.Fatalf("in use = %v, want [v1]", got)
	}

	// An update switches to v2. The chat reconnects live to its old process:
	// the release carries over before the new generation is written, so the
	// launch trigger keeps it on v1 instead of claiming v2.
	if err := s.SetManagedHarnessRelease(ctx, domain.HarnessCodex, "v2", now); err != nil {
		t.Fatal(err)
	}
	if err := s.CarryHarnessRelease(ctx, chat.ID, "gen-1", "gen-2"); err != nil {
		t.Fatal(err)
	}
	current, _, _ = s.GetSession(ctx, chat.ID)
	current.Metadata.ControllerGeneration = "gen-2"
	if err := s.UpdateSession(ctx, current); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneSessionHarnessReleases(ctx); err != nil {
		t.Fatal(err)
	}
	if got := inUse(); !slices.Equal(got, []string{"v1"}) {
		t.Fatalf("after a live reconnect in use = %v, want [v1]", got)
	}

	// A real relaunch starts the current release, and v1 is free.
	current, _, _ = s.GetSession(ctx, chat.ID)
	current.Metadata.ControllerGeneration = "gen-3"
	if err := s.UpdateSession(ctx, current); err != nil {
		t.Fatal(err)
	}
	terminals, _ := s.ListAllSessions(ctx)
	for _, session := range terminals {
		if session.Mode == domain.SessionModeTUI {
			session.Metadata.RuntimeLaunchID = "launch-2"
			if err := s.UpdateSession(ctx, session); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := inUse(); !slices.Equal(got, []string{"v2"}) {
		t.Fatalf("after relaunches in use = %v, want [v2]", got)
	}
}

func TestStaleMarkCarriesAcrossALiveReconnect(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	rec := sampleRecord("mer")
	rec.Harness, rec.Mode, rec.Metadata.ControllerGeneration = domain.HarnessCodex, domain.SessionModeChat, "gen-1"
	chat, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkHarnessSessionsStale(ctx, domain.HarnessCodex, "2.0.0", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	current, _, _ := s.GetSession(ctx, chat.ID)
	current.Metadata.ControllerGeneration = "gen-2"
	if err := s.UpdateSession(ctx, current); err != nil {
		t.Fatal(err)
	}
	if err := s.CarryStaleBinary(ctx, chat.ID, "gen-1", "gen-2"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.GetSession(ctx, chat.ID); got.StaleBinary == nil || got.StaleBinary.Version != "2.0.0" {
		t.Fatalf("stale mark after a live reconnect = %+v", got.StaleBinary)
	}
}
