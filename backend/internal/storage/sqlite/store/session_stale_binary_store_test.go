package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestStaleBinaryMarkAppliesOnlyToLiveLaunchesOfTheHarness(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	create := func(change func(*domain.SessionRecord)) domain.SessionID {
		rec := sampleRecord("mer")
		rec.Mode, rec.Metadata.ControllerGeneration = domain.SessionModeChat, "gen-1"
		change(&rec)
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	live := create(func(*domain.SessionRecord) {})
	tui := create(func(r *domain.SessionRecord) { r.Mode, r.Metadata.RuntimeLaunchID = domain.SessionModeTUI, "launch-1" })
	others := []domain.SessionID{
		create(func(r *domain.SessionRecord) { r.Harness = domain.HarnessCodex }),
		create(func(r *domain.SessionRecord) { r.IsTerminated = true }),
		create(func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityExited }),
	}
	// The sleep marker is written on its own, never by CreateSession.
	asleep := create(func(*domain.SessionRecord) {})
	sleeping, _, _ := s.GetSession(ctx, asleep)
	if applied, err := s.SetSessionHibernated(ctx, asleep, sleeping.Revision, &now); err != nil || !applied {
		t.Fatalf("hibernate = %v, %v", applied, err)
	}
	others = append(others, asleep)

	marked, err := s.MarkHarnessSessionsStale(ctx, domain.HarnessClaudeCode, "2.1.300", now)
	if err != nil || marked != 2 {
		t.Fatalf("marked = %d, %v; want the chat and terminal launch", marked, err)
	}
	for _, id := range []domain.SessionID{live, tui} {
		rec, _, _ := s.GetSession(ctx, id)
		if rec.StaleBinary == nil || rec.StaleBinary.Version != "2.1.300" || !rec.StaleBinary.Since.Equal(now) {
			t.Fatalf("%s stale = %+v", id, rec.StaleBinary)
		}
	}
	for _, id := range others {
		if rec, _, _ := s.GetSession(ctx, id); rec.StaleBinary != nil {
			t.Fatalf("%s was marked: %+v", id, rec.StaleBinary)
		}
	}
	counts, err := s.CountStaleBinarySessions(ctx)
	if err != nil || counts[domain.HarnessClaudeCode] != 2 {
		t.Fatalf("counts = %v, %v", counts, err)
	}

	// A second update moves the version on but keeps when the launch went stale.
	later := now.Add(time.Hour)
	if _, err := s.MarkHarnessSessionsStale(ctx, domain.HarnessClaudeCode, "2.1.301", later); err != nil {
		t.Fatal(err)
	}
	rec, _, _ := s.GetSession(ctx, live)
	if rec.StaleBinary.Version != "2.1.301" || !rec.StaleBinary.Since.Equal(now) {
		t.Fatalf("re-marked = %+v", rec.StaleBinary)
	}

	// Any new launch runs the installed release: the mark no longer applies.
	rec.Metadata.ControllerGeneration = "gen-2"
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if rec, _, _ = s.GetSession(ctx, live); rec.StaleBinary != nil {
		t.Fatalf("new chat launch still stale: %+v", rec.StaleBinary)
	}
	terminal, _, _ := s.GetSession(ctx, tui)
	terminal.Metadata.RuntimeLaunchID = "launch-2"
	if err := s.UpdateSession(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	if terminal, _, _ = s.GetSession(ctx, tui); terminal.StaleBinary != nil {
		t.Fatalf("new terminal launch still stale: %+v", terminal.StaleBinary)
	}
	if counts, _ := s.CountStaleBinarySessions(ctx); counts[domain.HarnessClaudeCode] != 0 {
		t.Fatalf("counts after relaunch = %v", counts)
	}
}

func TestVersionRefreshChatCandidatesWaitForQuietAndSkipFailedSwitches(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	create := func(lastActivity time.Time) domain.SessionID {
		rec := sampleRecord("mer")
		rec.Mode, rec.Metadata.ControllerGeneration, rec.Metadata.ProviderConversationID = domain.SessionModeChat, "gen-1", "native-id"
		rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: lastActivity}
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	quiet := create(now.Add(-20 * time.Minute))
	recent := create(now.Add(-time.Minute))
	failed := create(now.Add(-20 * time.Minute))
	if got, _ := s.ListVersionRefreshChatCandidates(ctx, now.Add(-10*time.Minute)); len(got) != 0 {
		t.Fatalf("unmarked candidates = %v", got)
	}
	if _, err := s.MarkHarnessSessionsStale(ctx, domain.HarnessClaudeCode, "2.1.300", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionVersionSwitchFailed(ctx, failed, "2.1.300", now); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListVersionRefreshChatCandidates(ctx, now.Add(-10*time.Minute))
	if err != nil || !slices.Equal(got, []domain.SessionID{quiet}) {
		t.Fatalf("candidates = %v, %v; want only %s (not recent %s or failed %s)", got, err, quiet, recent, failed)
	}
	rec, _, _ := s.GetSession(ctx, failed)
	if rec.StaleBinary == nil || rec.StaleBinary.SwitchFailedAt == nil {
		t.Fatalf("failed switch = %+v", rec.StaleBinary)
	}
}

// An orchestrator moves like any Chat session: the move starts it again right
// away, so idle hibernation keeping orchestrators awake does not apply.
func TestVersionRefreshChatCandidatesIncludeOrchestrators(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	rec := sampleRecord("mer")
	rec.Kind = domain.KindOrchestrator
	rec.Mode, rec.Metadata.ControllerGeneration, rec.Metadata.ProviderConversationID = domain.SessionModeChat, "gen-1", "native-id"
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-20 * time.Minute)}
	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkHarnessSessionsStale(ctx, domain.HarnessClaudeCode, "2.1.308", now); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListVersionRefreshChatCandidates(ctx, now.Add(-10*time.Minute))
	if err != nil || !slices.Equal(got, []domain.SessionID{created.ID}) {
		t.Fatalf("candidates = %v, %v; want the idle stale orchestrator %s", got, err, created.ID)
	}
}

func TestVersionRefreshSettingDefaultsOn(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	settings, err := s.GetAppSettings(ctx)
	if err != nil || !settings.VersionRefresh {
		t.Fatalf("default version refresh = %t, %v", settings.VersionRefresh, err)
	}
	if err := s.SetVersionRefresh(ctx, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if settings, err = s.GetAppSettings(ctx); err != nil || settings.VersionRefresh {
		t.Fatalf("version refresh after off = %t, %v", settings.VersionRefresh, err)
	}
}
