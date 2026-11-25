package automation

import (
	"context"
	"errors"
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

func (f *schedulerMemoryStore) MaterializeAutomationRuns(_ context.Context, id domain.AutomationID, expected time.Time, runs []domain.AutomationRun, last, next, updated time.Time) (bool, error) {
	rec := f.automations[id]
	if !rec.Enabled || !rec.NextRunAt.Equal(expected) {
		return false, nil
	}
	for _, run := range runs {
		f.runs[run.ID] = run
	}
	rec.LastRunAt, rec.NextRunAt, rec.UpdatedAt = &last, next, updated
	f.automations[id] = rec
	return true, nil
}

// ListAutomationRuns mirrors the SQL contract: newest first, optional status
// filter, limit/offset paging, and the filtered total.
func (f *schedulerMemoryStore) ListAutomationRuns(_ context.Context, filter domain.AutomationRunFilter) (domain.AutomationRunPage, error) {
	var matched []domain.AutomationRun
	for _, run := range f.runs {
		if run.AutomationID == filter.AutomationID && (filter.Status == nil || run.Status == *filter.Status) {
			matched = append(matched, run)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].ScheduledFor.Equal(matched[j].ScheduledFor) {
			return matched[i].ScheduledFor.After(matched[j].ScheduledFor)
		}
		return matched[i].ID > matched[j].ID
	})
	page := domain.AutomationRunPage{Total: int64(len(matched))}
	start := min(filter.Offset, int64(len(matched)))
	end := int64(len(matched))
	if filter.Limit > 0 {
		end = min(start+filter.Limit, end)
	}
	page.Items = matched[start:end]
	return page, nil
}

func (f *schedulerMemoryStore) runsWithStatus(id domain.AutomationID, status domain.AutomationRunStatus) []domain.AutomationRun {
	var out []domain.AutomationRun
	for _, run := range f.runs {
		if run.AutomationID == id && run.Status == status {
			out = append(out, run)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ScheduledFor.Before(out[j].ScheduledFor) })
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

func (f *schedulerMemoryStore) MarkAutomationRunRunning(_ context.Context, id domain.AutomationRunID, sessionID domain.SessionID, now time.Time) (bool, error) {
	run := f.runs[id]
	if run.Status != domain.AutomationRunSpawning {
		return false, nil
	}
	run.Status, run.SessionID, run.StartedAt, run.LeaseExpiresAt, run.UpdatedAt = domain.AutomationRunRunning, &sessionID, &now, nil, now
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
	killErr        error
	killFreed      bool
	spawnErr       error
	leaveSeedOnErr bool
}

func (s *recordingSpawner) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	s.calls = append(s.calls, cfg)
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
// falls due must not add another pending row: one owed run is enough.
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
	if len(pending) != 1 || !pending[0].ScheduledFor.Equal(base) {
		t.Fatalf("pending = %#v, want exactly the first owed occurrence", pending)
	}
	if store.runs[runID].Status != domain.AutomationRunRunning || len(spawner.calls) != 0 {
		t.Fatalf("busy run = %#v spawns = %d", store.runs[runID], len(spawner.calls))
	}
	if got := store.automations["automation-1"].NextRunAt; !got.Equal(base.Add(4 * time.Hour)) {
		t.Fatalf("next run = %s, want %s", got, base.Add(4*time.Hour))
	}
}

// A backlog left by earlier versions collapses to its oldest pending run the
// next time the automation falls due; the rest are recorded as skipped.
func TestTickCollapsesExistingPendingBacklog(t *testing.T) {
	base := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	now := base.Add(5 * time.Hour)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", RRuleText: "DTSTART:20260825T090000Z\nRRULE:FREQ=HOURLY", Timezone: "UTC", Enabled: true, NextRunAt: now}
	runID, sessionID := domain.AutomationRunID("run-busy"), domain.SessionID("session-busy")
	store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: base.Add(-time.Hour), SessionID: &sessionID, Status: domain.AutomationRunRunning}
	store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, AutomationRunID: &runID, FirstSignalAt: base, Activity: domain.Activity{State: domain.ActivityBlocked, LastActivityAt: base.Add(time.Minute)}}
	for hour := 0; hour < 5; hour++ {
		id := domain.AutomationRunID("run-backlog-" + string(rune('a'+hour)))
		store.runs[id] = domain.AutomationRun{ID: id, AutomationID: "automation-1", ScheduledFor: base.Add(time.Duration(hour) * time.Hour), Status: domain.AutomationRunPending}
	}
	spawner := &recordingSpawner{store: store}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	pending := store.runsWithStatus("automation-1", domain.AutomationRunPending)
	if len(pending) != 1 || pending[0].ID != "run-backlog-a" {
		t.Fatalf("pending = %#v, want only the oldest backlog run", pending)
	}
	skipped := store.runsWithStatus("automation-1", domain.AutomationRunFailed)
	if len(skipped) != 4 {
		t.Fatalf("skipped = %d, want 4", len(skipped))
	}
	for _, run := range skipped {
		if run.ErrorMessage != skippedRunMessage {
			t.Fatalf("skipped run %s message = %q", run.ID, run.ErrorMessage)
		}
	}
	if len(store.runs) != 6 || len(spawner.calls) != 0 {
		t.Fatalf("runs=%d spawns=%d, want no new row and no spawn while blocked", len(store.runs), len(spawner.calls))
	}
}

// A session that has finished its work completes its run without being
// killed, so the next occurrence starts unattended.
func TestTickCompletesSettledSessionWithoutKilling(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	firstSignal := now.Add(-30 * time.Minute)
	for _, state := range []domain.ActivityState{domain.ActivityIdle, domain.ActivityWaitingInput, domain.ActivityExited} {
		t.Run(string(state), func(t *testing.T) {
			store := newSchedulerStore()
			store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
			runID, sessionID := domain.AutomationRunID("run-1"), domain.SessionID("session-1")
			store.runs[runID] = domain.AutomationRun{ID: runID, AutomationID: "automation-1", ScheduledFor: now.Add(-time.Hour), SessionID: &sessionID, Status: domain.AutomationRunRunning}
			store.runs["run-2"] = domain.AutomationRun{ID: "run-2", AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunPending}
			store.sessions[sessionID] = domain.SessionRecord{ID: sessionID, AutomationRunID: &runID, FirstSignalAt: firstSignal, Activity: domain.Activity{State: state, LastActivityAt: firstSignal.Add(10 * time.Minute)}}
			spawner := &recordingSpawner{store: store}
			svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

			if err := svc.Tick(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			if store.runs[runID].Status != domain.AutomationRunCompleted || store.runs["run-2"].Status != domain.AutomationRunRunning || len(spawner.calls) != 1 {
				t.Fatalf("runs=%#v calls=%d", store.runs, len(spawner.calls))
			}
			if len(spawner.kills) != 0 || store.sessions[sessionID].IsTerminated {
				t.Fatalf("settled session was killed: kills=%v", spawner.kills)
			}
		})
	}
}

// A session that has not yet worked on its prompt, is still working, or is
// stopped on a permission prompt keeps its run and the gate closed.
func TestTickKeepsRunWhileSessionHasNotSettled(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	firstSignal := now.Add(-30 * time.Minute)
	cases := map[string]domain.SessionRecord{
		"seeded idle without signal": {Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: firstSignal}},
		"first signal only":          {FirstSignalAt: firstSignal, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: firstSignal}},
		"working":                    {FirstSignalAt: firstSignal, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}},
		"blocked on approval":        {FirstSignalAt: firstSignal, Activity: domain.Activity{State: domain.ActivityBlocked, LastActivityAt: now}},
		"still provisioning":         {FirstSignalAt: firstSignal, ProvisionState: domain.SessionProvisionProvisioning, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}},
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
			if store.runs[runID].Status != domain.AutomationRunRunning || store.runs["run-2"].Status != domain.AutomationRunPending || len(spawner.calls) != 0 {
				t.Fatalf("runs=%#v calls=%d", store.runs, len(spawner.calls))
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
