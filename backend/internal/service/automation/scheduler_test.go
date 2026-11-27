package automation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type schedulerMemoryStore struct {
	*fakeStore
	runs     map[domain.AutomationRunID]domain.AutomationRun
	sessions map[domain.SessionID]domain.SessionRecord
}

func newSchedulerStore() *schedulerMemoryStore {
	return &schedulerMemoryStore{fakeStore: newFakeStore(), runs: map[domain.AutomationRunID]domain.AutomationRun{}, sessions: map[domain.SessionID]domain.SessionRecord{}}
}

func (f *schedulerMemoryStore) ListDueAutomations(_ context.Context, now time.Time, _ int64) ([]domain.Automation, error) {
	var out []domain.Automation
	for _, rec := range f.automations {
		if rec.Enabled && !rec.NextRunAt.After(now) {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextRunAt.Before(out[j].NextRunAt) })
	return out, nil
}

// MaterializeAutomationRun mirrors the SQL contract: compare-and-swap on the
// cursor, then insert the run, or move the oldest pending run to its time and
// skip the other pending runs.
func (f *schedulerMemoryStore) MaterializeAutomationRun(_ context.Context, id domain.AutomationID, expected time.Time, run domain.AutomationRun, next, updated time.Time) (bool, error) {
	rec := f.automations[id]
	if !rec.Enabled || !rec.NextRunAt.Equal(expected) {
		return false, nil
	}
	if pending := f.runsWithStatus(id, domain.AutomationRunPending); len(pending) == 0 {
		f.runs[run.ID] = run
	} else {
		kept := pending[0]
		for _, candidate := range pending {
			if candidate.ScheduledFor.Equal(run.ScheduledFor) {
				kept = candidate
			}
		}
		kept.ScheduledFor, kept.UpdatedAt = run.ScheduledFor, updated
		f.runs[kept.ID] = kept
		for _, extra := range pending {
			if extra.ID == kept.ID {
				continue
			}
			extra.Status, extra.FinishedAt, extra.UpdatedAt = domain.AutomationRunSkipped, &updated, updated
			f.runs[extra.ID] = extra
		}
	}
	last := run.ScheduledFor
	rec.LastRunAt, rec.NextRunAt, rec.UpdatedAt = &last, next, updated
	f.automations[id] = rec
	return true, nil
}

func (f *schedulerMemoryStore) runsWithStatus(id domain.AutomationID, status domain.AutomationRunStatus) []domain.AutomationRun {
	var out []domain.AutomationRun
	for _, run := range f.runs {
		if run.AutomationID == id && run.Status == status {
			out = append(out, run)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ScheduledFor.Equal(out[j].ScheduledFor) {
			return out[i].ScheduledFor.Before(out[j].ScheduledFor)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (f *schedulerMemoryStore) ClaimNextAutomationRun(_ context.Context, now, lease time.Time) (domain.AutomationRun, bool, error) {
	active := map[domain.AutomationID]bool{}
	for _, run := range f.runs {
		if run.Status == domain.AutomationRunSpawning || run.Status == domain.AutomationRunRunning {
			active[run.AutomationID] = true
		}
	}
	var candidates []domain.AutomationRun
	for _, run := range f.runs {
		if run.Status == domain.AutomationRunPending && f.automations[run.AutomationID].Enabled && !active[run.AutomationID] {
			candidates = append(candidates, run)
		}
	}
	if len(candidates) == 0 {
		return domain.AutomationRun{}, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ScheduledFor.Before(candidates[j].ScheduledFor) })
	run := candidates[0]
	run.Status, run.AttemptCount, run.ClaimedAt, run.LeaseExpiresAt, run.UpdatedAt = domain.AutomationRunSpawning, run.AttemptCount+1, &now, &lease, now
	f.runs[run.ID] = run
	return run, true, nil
}

func (f *schedulerMemoryStore) ListActiveAutomationRuns(context.Context) ([]domain.AutomationRun, error) {
	var out []domain.AutomationRun
	for _, run := range f.runs {
		if run.Status == domain.AutomationRunSpawning || run.Status == domain.AutomationRunRunning {
			out = append(out, run)
		}
	}
	return out, nil
}

func (f *schedulerMemoryStore) GetSessionByAutomationRunID(_ context.Context, id domain.AutomationRunID) (domain.SessionRecord, bool, error) {
	for _, session := range f.sessions {
		if session.AutomationRunID != nil && *session.AutomationRunID == id {
			return session, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

func (f *schedulerMemoryStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := f.sessions[id]
	return rec, ok, nil
}

func (f *schedulerMemoryStore) MarkAutomationRunRunning(_ context.Context, id domain.AutomationRunID, sessionID domain.SessionID, startedAt, now time.Time) (bool, error) {
	run := f.runs[id]
	if run.Status != domain.AutomationRunSpawning {
		return false, nil
	}
	run.Status, run.SessionID, run.StartedAt, run.LeaseExpiresAt, run.UpdatedAt = domain.AutomationRunRunning, &sessionID, &startedAt, nil, now
	f.runs[id] = run
	return true, nil
}

func (f *schedulerMemoryStore) CompleteAutomationRun(_ context.Context, id domain.AutomationRunID, now time.Time) (bool, error) {
	run := f.runs[id]
	if run.Status != domain.AutomationRunRunning {
		return false, nil
	}
	run.Status, run.FinishedAt, run.UpdatedAt = domain.AutomationRunCompleted, &now, now
	f.runs[id] = run
	return true, nil
}

func (f *schedulerMemoryStore) FailAutomationRun(_ context.Context, id domain.AutomationRunID, message string, now time.Time) (bool, error) {
	run := f.runs[id]
	run.Status, run.ErrorMessage, run.FinishedAt, run.UpdatedAt = domain.AutomationRunFailed, message, &now, now
	f.runs[id] = run
	return true, nil
}

func (f *schedulerMemoryStore) ReleaseAutomationRun(_ context.Context, id domain.AutomationRunID, message string, now time.Time) (bool, error) {
	run := f.runs[id]
	run.Status, run.ErrorMessage, run.ClaimedAt, run.LeaseExpiresAt, run.UpdatedAt = domain.AutomationRunPending, message, nil, nil, now
	f.runs[id] = run
	return true, nil
}

type recordingSpawner struct {
	store          *schedulerMemoryStore
	calls          []ports.SpawnConfig
	kills          []domain.SessionID
	hibernations   []domain.SessionID
	hibernateBusy  bool
	hibernateErr   error
	onSpawn        func()
	killErr        error
	killFreed      bool
	killRefused    bool
	spawnErr       error
	leaveSeedOnErr bool
}

func (s *recordingSpawner) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	s.calls = append(s.calls, cfg)
	if s.onSpawn != nil {
		s.onSpawn()
	}
	id := domain.SessionID("scheduled-session-" + string(rune('1'+len(s.calls)-1)))
	if s.spawnErr != nil {
		if s.leaveSeedOnErr {
			s.store.sessions[id] = domain.SessionRecord{ID: id, ProjectID: cfg.ProjectID, AutomationRunID: cfg.AutomationRunID}
		}
		return domain.Session{}, 0, 0, s.spawnErr
	}
	rec := domain.SessionRecord{ID: id, ProjectID: cfg.ProjectID, AutomationRunID: cfg.AutomationRunID}
	s.store.sessions[id] = rec
	return domain.Session{SessionRecord: rec}, 0, 0, nil
}

func (s *recordingSpawner) HibernateChatForAutomation(_ context.Context, id domain.SessionID) (bool, error) {
	s.hibernations = append(s.hibernations, id)
	if s.hibernateErr != nil || s.hibernateBusy {
		return false, s.hibernateErr
	}
	rec := s.store.sessions[id]
	at := time.Now()
	rec.HibernatedAt = &at
	s.store.sessions[id] = rec
	return true, nil
}

func (s *recordingSpawner) KillSettledForAutomation(ctx context.Context, read domain.SessionRecord) (bool, error) {
	if s.killRefused {
		s.kills = append(s.kills, read.ID)
		return false, nil
	}
	if _, err := s.Kill(ctx, read.ID); err != nil {
		return false, err
	}
	return true, nil
}

func (s *recordingSpawner) Kill(_ context.Context, id domain.SessionID) (bool, error) {
	s.kills = append(s.kills, id)
	if s.killErr != nil {
		return false, s.killErr
	}
	rec, ok := s.store.sessions[id]
	if ok {
		rec.IsTerminated = true
		s.store.sessions[id] = rec
	}
	return s.killFreed, nil
}

// Missed occurrences coalesce into one durable row: the schedule advances past
// the whole missed window and the most recent missed occurrence is dispatched.
func TestTickCoalescesMissedOccurrencesIntoOneRun(t *testing.T) {
	base := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	now := base.Add(5 * time.Hour)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", DisplayName: "Hourly", Prompt: "Review", Model: "gpt-5", Effort: "high", Mode: "agent", Kind: domain.KindWorker, RRuleText: "DTSTART:20260825T090000Z\nRRULE:FREQ=HOURLY", Timezone: "UTC", Enabled: true, NextRunAt: base}
	spawner := &recordingSpawner{store: store}
	sequence := 0
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }, NewID: func() string { sequence++; return string(rune('a' + sequence - 1)) }})

	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(store.runs) != 1 || len(spawner.calls) != 1 {
		t.Fatalf("runs=%d spawns=%d, want 1 and 1", len(store.runs), len(spawner.calls))
	}
	if got := store.automations["automation-1"].NextRunAt; !got.Equal(base.Add(6 * time.Hour)) {
		t.Fatalf("next run = %s, want %s", got, base.Add(6*time.Hour))
	}
	if got := spawner.calls[0].AgentConfig; got.Model != "gpt-5" || got.Effort != "high" || got.Mode != "agent" {
		t.Fatalf("agent config = %#v", got)
	}
	if spawner.calls[0].AutomationRunID == nil || !store.runs[*spawner.calls[0].AutomationRunID].ScheduledFor.Equal(now) {
		t.Fatalf("spawn config = %#v, want most recent missed occurrence %s", spawner.calls[0], now)
	}
}

// Between occurrences the catch-up run is for the latest one already due, not
// the first one missed.
func TestTickCatchupUsesLatestDueOccurrenceBetweenTicks(t *testing.T) {
	base := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	now := base.Add(5*time.Hour + 20*time.Minute)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", RRuleText: "DTSTART:20260825T090000Z\nRRULE:FREQ=HOURLY", Timezone: "UTC", Enabled: true, NextRunAt: base}
	spawner := &recordingSpawner{store: store}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(store.runs) != 1 || len(spawner.calls) != 1 || spawner.calls[0].AutomationRunID == nil {
		t.Fatalf("runs=%d spawns=%d, want one run dispatched", len(store.runs), len(spawner.calls))
	}
	if got := store.runs[*spawner.calls[0].AutomationRunID].ScheduledFor; !got.Equal(base.Add(5 * time.Hour)) {
		t.Fatalf("scheduled for %s, want %s", got, base.Add(5*time.Hour))
	}
	rec := store.automations["automation-1"]
	if rec.LastRunAt == nil || !rec.LastRunAt.Equal(base.Add(5*time.Hour)) || !rec.NextRunAt.Equal(base.Add(6*time.Hour)) {
		t.Fatalf("automation last=%v next=%s", rec.LastRunAt, rec.NextRunAt)
	}
}

// While an automation's session is still working, each new occurrence that
// falls due must not add another pending row: the one waiting run moves to
// the latest due time instead.
func TestTickKeepsOnePendingRunWhileAutomationIsBusy(t *testing.T) {
	base := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	now := base
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", RRuleText: "DTSTART:20260825T090000Z\nRRULE:FREQ=HOURLY", Timezone: "UTC", Enabled: true, NextRunAt: base}
	runID, sessionID := domain.AutomationRunID("run-busy"), domain.SessionID("session-busy")
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: base.Add(-time.Hour), SessionID: &sessionID, Status: domain.AutomationRunRunning}
	store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, AutomationRunID: &runID, FirstSignalAt: base.Add(-time.Hour), Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: base.Add(-time.Minute)}}
	spawner := &recordingSpawner{store: store}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	for hour := 0; hour < 4; hour++ {
		now = base.Add(time.Duration(hour) * time.Hour)
		if err := svc.Tick(context.Background(), now); err != nil {
			t.Fatalf("Tick at %s: %v", now, err)
		}
	}
	pending := store.runsWithStatus("automation-1", domain.AutomationRunPending)
	if len(pending) != 1 || !pending[0].ScheduledFor.Equal(base.Add(3*time.Hour)) {
		t.Fatalf("pending = %#v, want exactly one run for the latest due time", pending)
	}
	if store.runs[runID].Status != domain.AutomationRunRunning || len(spawner.calls) != 0 {
		t.Fatalf("busy run = %#v spawns = %d", store.runs[runID], len(spawner.calls))
	}
	rec := store.automations["automation-1"]
	if !rec.NextRunAt.Equal(base.Add(4*time.Hour)) || rec.LastRunAt == nil || !rec.LastRunAt.Equal(pending[0].ScheduledFor) {
		t.Fatalf("automation next=%s last=%v, want next %s and last matching the waiting run", rec.NextRunAt, rec.LastRunAt, base.Add(4*time.Hour))
	}
}

// A backlog left by earlier versions collapses the next time the automation
// falls due: the oldest pending run is kept, moved to the latest due time, and
// the rest are recorded as skipped, not failed. The dispatched run is the kept
// one, once the busy session has settled.
func TestTickCollapsesExistingPendingBacklog(t *testing.T) {
	base := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	now := base.Add(200 * time.Hour)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", RRuleText: "DTSTART:20260801T000000Z\nRRULE:FREQ=HOURLY", Timezone: "UTC", Enabled: true, NextRunAt: now}
	runID, sessionID := domain.AutomationRunID("run-busy"), domain.SessionID("session-busy")
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: base.Add(-time.Hour), SessionID: &sessionID, Status: domain.AutomationRunRunning}
	store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, AutomationRunID: &runID, FirstSignalAt: base, Activity: domain.Activity{State: domain.ActivityBlocked, LastActivityAt: base.Add(time.Minute)}}
	const backlog = 101
	for i := 0; i < backlog; i++ {
		id := domain.AutomationRunID(fmt.Sprintf("run-backlog-%03d", i))
		store.runs[id] = domain.AutomationRun{ID: id, AutomationID: "automation-1", ScheduledFor: base.Add(time.Duration(i) * time.Hour), Status: domain.AutomationRunPending}
	}
	spawner := &recordingSpawner{store: store}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	pending := store.runsWithStatus("automation-1", domain.AutomationRunPending)
	if len(pending) != 1 || pending[0].ID != "run-backlog-000" || !pending[0].ScheduledFor.Equal(now) {
		t.Fatalf("pending = %d runs (first %v), want only run-backlog-000 moved to %s", len(pending), pending, now)
	}
	if skipped := store.runsWithStatus("automation-1", domain.AutomationRunSkipped); len(skipped) != backlog-1 {
		t.Fatalf("skipped = %d, want %d", len(skipped), backlog-1)
	}
	if failed := store.runsWithStatus("automation-1", domain.AutomationRunFailed); len(failed) != 0 {
		t.Fatalf("failed = %d, want none: skipped runs are not failures", len(failed))
	}
	if len(spawner.calls) != 0 {
		t.Fatalf("spawns = %d, want none while the session is blocked", len(spawner.calls))
	}

	// The busy session settles; tick again before the next occurrence.
	session := store.sessions[sessionID]
	session.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
	store.sessions[sessionID] = session
	now = now.Add(10 * time.Minute)
	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(spawner.calls) != 1 || spawner.calls[0].AutomationRunID == nil || *spawner.calls[0].AutomationRunID != "run-backlog-000" {
		t.Fatalf("spawns = %#v, want only the kept run dispatched", spawner.calls)
	}
	if left := store.runsWithStatus("automation-1", domain.AutomationRunPending); len(left) != 0 {
		t.Fatalf("pending after dispatch = %d, want 0", len(left))
	}
}

// A settled session is stopped before its run completes, so it cannot start
// working again beside the next occurrence. Terminal sessions are killed;
// Chat sessions are hibernated and stay resumable, unless the provider cannot
// resume or the agent process has exited, which falls back to a kill.
func TestTickStopsSettledSessionBeforeCompletingRun(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	firstSignal := now.Add(-30 * time.Minute)
	for _, tc := range []struct {
		name          string
		mode          domain.SessionMode
		state         domain.ActivityState
		unsupported   bool
		wantKill      bool
		wantHibernate bool
	}{
		{name: "terminal idle", mode: domain.SessionModeTUI, state: domain.ActivityIdle, wantKill: true},
		{name: "terminal exited", mode: domain.SessionModeTUI, state: domain.ActivityExited, wantKill: true},
		{name: "chat idle", mode: domain.SessionModeChat, state: domain.ActivityIdle, wantHibernate: true},
		{name: "chat exited", mode: domain.SessionModeChat, state: domain.ActivityExited, wantKill: true},
		{name: "chat provider cannot resume", mode: domain.SessionModeChat, state: domain.ActivityIdle, unsupported: true, wantKill: true, wantHibernate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newSchedulerStore()
			store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
			runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
			store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now.Add(-time.Hour), SessionID: &sessionID, Status: domain.AutomationRunRunning}
			store.runs["run-2"] = domain.AutomationRun{ID: "run-2", AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunPending}
			store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, Mode: tc.mode, AutomationRunID: &runID, FirstSignalAt: firstSignal, Activity: domain.Activity{State: tc.state, LastActivityAt: firstSignal.Add(10 * time.Minute)}}
			spawner := &recordingSpawner{store: store}
			if tc.unsupported {
				spawner.hibernateErr = ports.ErrChatHibernationUnsupported
			}
			svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

			if err := svc.Tick(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			if store.runs[runID].Status != domain.AutomationRunCompleted || store.runs["run-2"].Status != domain.AutomationRunRunning || len(spawner.calls) != 1 {
				t.Fatalf("runs=%#v calls=%d", store.runs, len(spawner.calls))
			}
			if killed := len(spawner.kills) == 1 && spawner.kills[0] == sessionID; killed != tc.wantKill || len(spawner.kills) > 1 {
				t.Fatalf("kills = %v, want kill=%v", spawner.kills, tc.wantKill)
			}
			if hibernated := len(spawner.hibernations) == 1; hibernated != tc.wantHibernate {
				t.Fatalf("hibernations = %v, want hibernate=%v", spawner.hibernations, tc.wantHibernate)
			}
		})
	}
}

// A Chat session that still has work (a queued message, an approval, an open
// view) refuses hibernation. Its run stays running and the next occurrence
// waits, rather than starting beside it.
func TestTickKeepsRunWhileSettledChatCannotHibernateYet(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	firstSignal := now.Add(-30 * time.Minute)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
	runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now.Add(-time.Hour), SessionID: &sessionID, Status: domain.AutomationRunRunning}
	store.runs["run-2"] = domain.AutomationRun{ID: "run-2", AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunPending}
	store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, Mode: domain.SessionModeChat, AutomationRunID: &runID, FirstSignalAt: firstSignal, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: firstSignal.Add(10 * time.Minute)}}
	spawner := &recordingSpawner{store: store, hibernateBusy: true}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if store.runs[runID].Status != domain.AutomationRunRunning || store.runs["run-2"].Status != domain.AutomationRunPending || len(spawner.calls) != 0 || len(spawner.kills) != 0 {
		t.Fatalf("runs=%#v calls=%d kills=%v", store.runs, len(spawner.calls), spawner.kills)
	}
}

// A terminal session that starts work after it was read as settled (a prompt
// delivered before the kill closed input) refuses the kill. Its run stays
// running and the next occurrence waits.
func TestTickKeepsRunWhenSettledSessionStartsWorkBeforeTheKill(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	firstSignal := now.Add(-30 * time.Minute)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
	runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now.Add(-time.Hour), SessionID: &sessionID, Status: domain.AutomationRunRunning}
	store.runs["run-2"] = domain.AutomationRun{ID: "run-2", AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunPending}
	store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, Mode: domain.SessionModeTUI, AutomationRunID: &runID, FirstSignalAt: firstSignal, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: firstSignal.Add(10 * time.Minute)}}
	spawner := &recordingSpawner{store: store, killRefused: true}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if store.runs[runID].Status != domain.AutomationRunRunning || store.runs["run-2"].Status != domain.AutomationRunPending || len(spawner.calls) != 0 || store.sessions[sessionID].IsTerminated {
		t.Fatalf("runs=%#v calls=%d session=%#v", store.runs, len(spawner.calls), store.sessions[sessionID])
	}
}

// A Chat session marked hibernated by a shutdown that then failed may still
// have a live provider. Its run waits for the stop to be finished instead of
// completing on the marker alone.
func TestTickFinishesAFailedHibernationBeforeCompletingRun(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	firstSignal := now.Add(-30 * time.Minute)
	marked := now.Add(-5 * time.Minute)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
	runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now.Add(-time.Hour), SessionID: &sessionID, Status: domain.AutomationRunRunning}
	store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, Mode: domain.SessionModeChat, AutomationRunID: &runID, HibernatedAt: &marked, FirstSignalAt: firstSignal, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: firstSignal.Add(10 * time.Minute)}}
	spawner := &recordingSpawner{store: store, hibernateErr: errors.New("provider host stop failed")}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err == nil || store.runs[runID].Status != domain.AutomationRunRunning {
		t.Fatalf("failed stop: err=%v run=%#v", err, store.runs[runID])
	}
	spawner.hibernateErr = nil
	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if store.runs[runID].Status != domain.AutomationRunCompleted || len(spawner.hibernations) != 2 || len(spawner.kills) != 0 {
		t.Fatalf("run=%#v hibernations=%v kills=%v", store.runs[runID], spawner.hibernations, spawner.kills)
	}
}

// Activity the agent reported before its run started (a seeded idle, a
// SessionStart, an empty Stop before the prompt was pasted) does not settle
// the run, even when it is later than the first signal. Only a reading after
// the start does.
func TestTickIgnoresActivityFromBeforeTheRunStarted(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	started := now.Add(-10 * time.Minute)
	for _, tc := range []struct {
		name    string
		reading time.Time
		settled bool
	}{
		{name: "second startup idle before the start", reading: started.Add(-time.Second), settled: false},
		{name: "idle exactly at the start", reading: started, settled: false},
		{name: "idle after the start", reading: started.Add(time.Minute), settled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newSchedulerStore()
			store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
			runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
			store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now.Add(-time.Hour), SessionID: &sessionID, Status: domain.AutomationRunRunning, StartedAt: &started}
			store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, AutomationRunID: &runID, FirstSignalAt: started.Add(-time.Minute), Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: tc.reading}}
			spawner := &recordingSpawner{store: store}
			svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

			if err := svc.Tick(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			if completed := store.runs[runID].Status == domain.AutomationRunCompleted; completed != tc.settled {
				t.Fatalf("run = %#v, want completed=%v", store.runs[runID], tc.settled)
			}
		})
	}
}

// Dispatch records the run's start after Spawn returns, so readings the agent
// sent while launching cannot settle it.
func TestTickRecordsRunStartAfterSpawnReturns(t *testing.T) {
	tick := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	clock := tick
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: tick.Add(time.Hour)}
	store.runs["run-1"] = domain.AutomationRun{ID: "run-1", AutomationID: "automation-1", ScheduledFor: tick, Status: domain.AutomationRunPending}
	spawner := &recordingSpawner{store: store, onSpawn: func() { clock = tick.Add(42 * time.Second) }}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return clock }})

	if err := svc.Tick(context.Background(), tick); err != nil {
		t.Fatal(err)
	}
	run := store.runs["run-1"]
	if run.Status != domain.AutomationRunRunning || run.StartedAt == nil || !run.StartedAt.Equal(tick.Add(42*time.Second)) {
		t.Fatalf("run = %#v, want running with start after the spawn returned", run)
	}
}

// A session that has not yet worked on its prompt, is still working, or is
// stopped on an approval or input request keeps its run and the gate closed.
func TestTickKeepsRunWhileSessionHasNotSettled(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	firstSignal := now.Add(-30 * time.Minute)
	// The chat controller reports chat.approval.requested and
	// chat.input.requested as waiting_input.
	cases := map[string]domain.SessionRecord{
		"seeded idle without signal":   {Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: firstSignal}},
		"first signal only":            {FirstSignalAt: firstSignal, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: firstSignal}},
		"working":                      {FirstSignalAt: firstSignal, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}},
		"blocked on approval":          {FirstSignalAt: firstSignal, Activity: domain.Activity{State: domain.ActivityBlocked, LastActivityAt: now}},
		"waiting on approval or input": {FirstSignalAt: firstSignal, Activity: domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: now}},
		"still provisioning":           {FirstSignalAt: firstSignal, ProvisionState: domain.SessionProvisionProvisioning, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}},
	}
	for name, session := range cases {
		t.Run(name, func(t *testing.T) {
			store := newSchedulerStore()
			store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
			runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
			store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now.Add(-time.Hour), SessionID: &sessionID, Status: domain.AutomationRunRunning}
			store.runs["run-2"] = domain.AutomationRun{ID: "run-2", AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunPending}
			session.ID, session.AutomationRunID = sessionID, &runID
			store.sessions[sessionID] = session
			spawner := &recordingSpawner{store: store}
			svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

			if err := svc.Tick(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			if store.runs[runID].Status != domain.AutomationRunRunning || store.runs["run-2"].Status != domain.AutomationRunPending || len(spawner.calls) != 0 || len(spawner.kills) != 0 {
				t.Fatalf("runs=%#v calls=%d kills=%v", store.runs, len(spawner.calls), spawner.kills)
			}
		})
	}
}

// Durable session termination, not a runtime probe, releases the non-overlap
// gate and allows the next queued occurrence to start.
func TestTickCompletesTerminatedSessionBeforeNextDispatch(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
	runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now.Add(-time.Hour), SessionID: &sessionID, Status: domain.AutomationRunRunning}
	store.runs["run-2"] = domain.AutomationRun{ID: "run-2", AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunPending}
	store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, AutomationRunID: &runID, IsTerminated: true}
	spawner := &recordingSpawner{store: store}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if store.runs[runID].Status != domain.AutomationRunCompleted || store.runs["run-2"].Status != domain.AutomationRunRunning || len(spawner.calls) != 1 {
		t.Fatalf("runs=%#v calls=%d", store.runs, len(spawner.calls))
	}
}

// Boot reconciliation adopts a crash-surviving idempotent session instead of
// releasing the occurrence and spawning a duplicate.
func TestReconcileAdoptsSessionForExpiredClaim(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", Enabled: true, NextRunAt: now.Add(time.Hour)}
	runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
	lease := now.Add(-time.Minute)
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", Status: domain.AutomationRunSpawning, LeaseExpiresAt: &lease}
	store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, AutomationRunID: &runID, AutomationLaunchCompleted: true}
	spawner := &recordingSpawner{store: store}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.runs[runID].Status != domain.AutomationRunRunning || store.runs[runID].SessionID == nil || *store.runs[runID].SessionID != sessionID || len(spawner.calls) != 0 {
		t.Fatalf("run=%#v calls=%d", store.runs[runID], len(spawner.calls))
	}
}

// A restart between a launch completing and the scheduler marking its run
// running must not use the recovery time as the start: a turn that finished
// while the daemon was down would never settle. The session's prompt time is
// the start, so its later idle settles the run while a startup idle from
// before the prompt still does not.
func TestReconcileStartsAdoptedRunAtThePromptNotTheRecovery(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	prompt := now.Add(-20 * time.Minute)
	for _, tc := range []struct {
		name    string
		reading time.Time
		settled bool
	}{
		{name: "idle after the prompt, before the restart", reading: prompt.Add(5 * time.Minute), settled: true},
		{name: "startup idle before the prompt", reading: prompt.Add(-time.Minute), settled: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newSchedulerStore()
			store.automations["automation-1"] = domain.Automation{ID: "automation-1", Enabled: true, NextRunAt: now.Add(time.Hour)}
			runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
			lease := now.Add(-time.Minute)
			store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", Status: domain.AutomationRunSpawning, LeaseExpiresAt: &lease}
			session := domain.SessionRecord{ID: sessionID, AutomationRunID: &runID, AutomationLaunchCompleted: true, FirstSignalAt: prompt.Add(-2 * time.Minute), Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: tc.reading}}
			session.Metadata.LatestUserPromptAt = prompt
			store.sessions[sessionID] = session
			spawner := &recordingSpawner{store: store}
			svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

			if err := svc.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			run := store.runs[runID]
			if run.StartedAt == nil || !run.StartedAt.Equal(prompt) {
				t.Fatalf("started at %v, want the prompt time %s", run.StartedAt, prompt)
			}
			if completed := run.Status == domain.AutomationRunCompleted; completed != tc.settled {
				t.Fatalf("run = %#v, want completed=%v", run, tc.settled)
			}
		})
	}
}

func TestReconcileCompletesTerminatedSessionForExpiredClaim(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
	lease := now.Add(-time.Minute)
	store.runs[runID] = domain.AutomationRun{ID: runID, Status: domain.AutomationRunSpawning, LeaseExpiresAt: &lease}
	store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, AutomationRunID: &runID, AutomationLaunchCompleted: true, IsTerminated: true}
	svc := New(Deps{Store: store, Clock: func() time.Time { return now }})

	if err := svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	run := store.runs[runID]
	if run.Status != domain.AutomationRunCompleted || run.FinishedAt == nil || !run.FinishedAt.Equal(now) {
		t.Fatalf("run = %#v, want completed at %s", run, now)
	}
}

// A seed row is not a completed launch. Reconciliation must tear it down and
// fail the occurrence instead of adopting it or starting a duplicate runtime.
func TestReconcileRollsBackIncompleteAutomationSession(t *testing.T) {
	now := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
	lease := now.Add(-time.Second)
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", Status: domain.AutomationRunSpawning, LeaseExpiresAt: &lease}
	store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, AutomationRunID: &runID}
	spawner := &recordingSpawner{store: store}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(spawner.kills) != 1 || spawner.kills[0] != sessionID {
		t.Fatalf("killed sessions = %v, want [%s]", spawner.kills, sessionID)
	}
	if got := store.runs[runID].Status; got != domain.AutomationRunFailed {
		t.Fatalf("run status = %s, want failed", got)
	}
}

// A restart before the lease expires skips startup reconciliation, so regular
// ticks must release an expired claim that never produced a session instead of
// leaving the run stuck in spawning and blocking every later occurrence.
func TestTickReleasesExpiredSpawningClaimWithoutSession(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	runID := domain.AutomationRunID("run-1")
	lease := now.Add(-time.Minute)
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", Status: domain.AutomationRunSpawning, LeaseExpiresAt: &lease}
	spawner := &recordingSpawner{store: store}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	run := store.runs[runID]
	if run.Status != domain.AutomationRunPending || run.LeaseExpiresAt != nil || run.ClaimedAt != nil {
		t.Fatalf("run = %#v, want released to pending", run)
	}
	if len(spawner.calls) != 0 {
		t.Fatalf("spawns = %d, want 0", len(spawner.calls))
	}
}

// If workspace provisioning dirties the worktree and rollback preserves the
// automation seed row, requeueing only creates an infinite
// "incomplete prior launch" loop. The scheduler must make that retained-row
// failure terminal for the occurrence.
func TestTickFailsRunWhenSpawnErrorLeavesIncompleteAutomationSession(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
	runID := domain.AutomationRunID("run-1")
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunPending}
	spawner := &recordingSpawner{
		store:          store,
		spawnErr:       apierr.Conflict("WORKSPACE_PROVISION_FAILED", "postCreate failed", nil),
		leaveSeedOnErr: true,
	}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err == nil {
		t.Fatal("Tick succeeded, want spawn diagnostic")
	}
	run := store.runs[runID]
	if run.Status != domain.AutomationRunFailed || run.ErrorMessage == "" {
		t.Fatalf("run = %#v, want terminal failure with message", run)
	}
	if len(spawner.calls) != 1 {
		t.Fatalf("spawns = %d, want 1", len(spawner.calls))
	}
	if len(spawner.kills) != 1 || spawner.kills[0] != "scheduled-session-1" {
		t.Fatalf("kills = %v, want retained session teardown", spawner.kills)
	}
}

func TestTickFailsRunWhenRetainedSessionKillPreservesWorkspace(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
	runID := domain.AutomationRunID("run-1")
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunPending}
	spawner := &recordingSpawner{
		store:          store,
		spawnErr:       errors.New("completion marker failed"),
		killFreed:      false,
		leaveSeedOnErr: true,
	}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err == nil {
		t.Fatal("Tick succeeded, want spawn diagnostic")
	}
	run := store.runs[runID]
	if run.Status != domain.AutomationRunFailed || run.FinishedAt == nil {
		t.Fatalf("run = %#v, want failed after successful teardown with preserved workspace", run)
	}
	if len(spawner.kills) != 1 || spawner.kills[0] != "scheduled-session-1" {
		t.Fatalf("kills = %v, want retained session teardown", spawner.kills)
	}
}

func TestTickLeavesRunRecoverableWhenRetainedSessionKillFails(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
	runID := domain.AutomationRunID("run-1")
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunPending}
	spawner := &recordingSpawner{
		store:          store,
		spawnErr:       errors.New("completion marker failed"),
		killErr:        errors.New("kill unavailable"),
		leaveSeedOnErr: true,
	}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err == nil {
		t.Fatal("Tick succeeded, want joined spawn/kill diagnostic")
	}
	run := store.runs[runID]
	if run.Status != domain.AutomationRunSpawning || run.FinishedAt != nil {
		t.Fatalf("run = %#v, want still spawning for lease recovery", run)
	}
	if len(spawner.kills) != 1 || spawner.kills[0] != "scheduled-session-1" {
		t.Fatalf("kills = %v, want attempted teardown", spawner.kills)
	}
}

func TestTickFailsTerminatedRetainedSeedWithoutKilling(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
	runID := domain.AutomationRunID("run-1")
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunPending}
	sessionID := domain.SessionID("scheduled-session-1")
	store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, ProjectID: "scheduled", AutomationRunID: &runID, IsTerminated: true}
	spawner := &recordingSpawner{store: store, spawnErr: errors.New("incomplete prior launch")}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err == nil {
		t.Fatal("Tick succeeded, want spawn diagnostic")
	}
	run := store.runs[runID]
	if run.Status != domain.AutomationRunFailed {
		t.Fatalf("run status = %s, want failed", run.Status)
	}
	if len(spawner.kills) != 0 {
		t.Fatalf("kills = %v, want no teardown for terminated seed", spawner.kills)
	}
}
