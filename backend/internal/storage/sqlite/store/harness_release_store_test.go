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
	current, _, _ := s.GetSession(ctx, chat.ID)
	current.Metadata.ControllerGeneration = "gen-1"
	if err := s.UpdateSession(ctx, current); err != nil {
		t.Fatal(err)
	}
	tuiRec := sampleRecord("mer")
	tuiRec.Harness, tuiRec.Mode, tuiRec.Metadata.RuntimeLaunchID = domain.HarnessCodex, domain.SessionModeTUI, "launch-1"
	if _, err := s.CreateSession(ctx, tuiRec); err != nil {
		t.Fatal(err)
	}
	if got := inUse(); !slices.Equal(got, []string{"v1"}) {
		t.Fatalf("in use = %v, want [v1]", got)
	}

	if err := s.SetManagedHarnessRelease(ctx, domain.HarnessCodex, "v2", now); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimLiveChatControllerGeneration(ctx, chat.ID, "gen-2"); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneSessionHarnessReleases(ctx); err != nil {
		t.Fatal(err)
	}
	if got := inUse(); !slices.Equal(got, []string{"v1"}) {
		t.Fatalf("after a live reconnect in use = %v, want [v1]", got)
	}

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

func TestLiveClaimKeepsTheOldReleaseThroughAPrune(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	now := time.Now().UTC()
	if err := s.SetManagedHarnessRelease(ctx, domain.HarnessCodex, "v1", now); err != nil {
		t.Fatal(err)
	}
	rec := sampleRecord("mer")
	rec.Harness, rec.Mode, rec.Metadata.ControllerGeneration = domain.HarnessCodex, domain.SessionModeChat, "gen-1"
	chat, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetManagedHarnessRelease(ctx, domain.HarnessCodex, "v2", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkHarnessSessionsStale(ctx, domain.HarnessCodex, "2.0.0", now); err != nil {
		t.Fatal(err)
	}

	if err := s.ClaimLiveChatControllerGeneration(ctx, chat.ID, "gen-2"); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneSessionHarnessReleases(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListHarnessReleasesInUse(ctx, domain.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"v1"}) {
		t.Fatalf("in use after a live claim and prune = %v, want [v1]", got)
	}
	current, _, _ := s.GetSession(ctx, chat.ID)
	if current.Metadata.ControllerGeneration != "gen-2" {
		t.Fatalf("generation = %q, want gen-2", current.Metadata.ControllerGeneration)
	}
	if current.StaleBinary == nil || current.StaleBinary.Version != "2.0.0" {
		t.Fatalf("stale mark after a live claim = %+v, want it kept on gen-2", current.StaleBinary)
	}
}

func TestFailedTerminalRefreshRetainsRelease(t *testing.T) {
	for _, harness := range domain.AllHarnesses {
		switch harness {
		case domain.HarnessAider, domain.HarnessContinue, domain.HarnessDeepSeek, domain.HarnessUnreal:
			continue
		}

		t.Run(string(harness), func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			seedProject(t, s, "mer")
			now := time.Now().UTC()
			if err := s.SetManagedHarnessRelease(ctx, harness, "v1", now); err != nil {
				t.Fatal(err)
			}
			rec := sampleRecord("mer")
			rec.Harness, rec.Mode, rec.Metadata.RuntimeLaunchID = harness, domain.SessionModeTUI, "launch-old"
			terminal, err := s.CreateSession(ctx, rec)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SetManagedHarnessRelease(ctx, harness, "v2", now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.MarkHarnessSessionsStale(ctx, harness, "new", now); err != nil {
				t.Fatal(err)
			}
			if err := s.SetSessionVersionSwitchFailed(ctx, terminal.ID, "new", now); err != nil {
				t.Fatal(err)
			}
			if err := s.PruneSessionHarnessReleases(ctx); err != nil {
				t.Fatal(err)
			}
			releases, err := s.ListHarnessReleasesInUse(ctx, harness)
			if err != nil || !slices.Equal(releases, []string{"v1"}) {
				t.Fatalf("failed refresh released old binary: %v, %v", releases, err)
			}
			current, _, err := s.GetSession(ctx, terminal.ID)
			if err != nil || current.StaleBinary == nil || current.StaleBinary.SwitchFailedAt == nil {
				t.Fatalf("failure notice missing: %+v, %v", current.StaleBinary, err)
			}
			current.Metadata.RuntimeLaunchID = "launch-success"
			if err := s.UpdateSession(ctx, current); err != nil {
				t.Fatal(err)
			}
			if err := s.PruneSessionHarnessReleases(ctx); err != nil {
				t.Fatal(err)
			}
			releases, err = s.ListHarnessReleasesInUse(ctx, harness)
			if err != nil || !slices.Equal(releases, []string{"v2"}) {
				t.Fatalf("successful refresh did not release v1: %v, %v", releases, err)
			}
			current, _, err = s.GetSession(ctx, terminal.ID)
			if err != nil || current.StaleBinary != nil {
				t.Fatalf("success still marked stale: %+v, %v", current.StaleBinary, err)
			}
		})
	}
}
