package automation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func sessionEndedStore() *schedulerMemoryStore {
	store := newSchedulerStore()
	for _, rec := range []domain.Automation{
		{ID: "review", ProjectID: "app", Trigger: domain.AutomationTriggerSessionEnded, Enabled: true},
		{ID: "paused-review", ProjectID: "app", Trigger: domain.AutomationTriggerSessionEnded},
		{ID: "nightly", ProjectID: "app", Trigger: domain.AutomationTriggerSchedule, Enabled: true},
		{ID: "other-review", ProjectID: "other", Trigger: domain.AutomationTriggerSessionEnded, Enabled: true},
	} {
		rec.DisplayName, rec.Prompt, rec.Kind = "Session review", "Suggest improvements to our skills.", domain.KindWorker
		rec.NextRunAt = time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
		store.automations[rec.ID] = rec
	}
	return store
}

// Only the project's enabled session-ended automations queue a run, and a
// second report of the same ended session must not queue a second review.
func TestSessionEndedQueuesOneRunPerSessionEndedAutomation(t *testing.T) {
	now := time.Date(2026, time.October, 13, 9, 0, 0, 0, time.UTC)
	store := sessionEndedStore()
	store.sessions["worker-1"] = domain.SessionRecord{ID: "worker-1", ProjectID: "app", Kind: domain.KindWorker}
	svc := New(Deps{Store: store, Clock: func() time.Time { return now }, NewID: func() string { return "1" }})

	for attempt, wantQueued := range []bool{true, false} {
		queued, err := svc.SessionEnded(context.Background(), "worker-1")
		if err != nil || queued != wantQueued {
			t.Fatalf("attempt %d: queued=%v err=%v, want %v", attempt, queued, err, wantQueued)
		}
	}
	if len(store.runs) != 1 {
		t.Fatalf("runs = %#v, want one", store.runs)
	}
	for _, run := range store.runs {
		if run.AutomationID != "review" || run.Status != domain.AutomationRunPending ||
			run.TriggerSessionID == nil || *run.TriggerSessionID != "worker-1" || !run.ScheduledFor.Equal(now) {
			t.Fatalf("run = %#v, want a pending review of worker-1", run)
		}
	}
}

// Sessions that never became a working worker, and sessions that a
// session-ended run started, must not queue a review; the second rule is what
// stops reviews from reviewing themselves forever.
func TestSessionEndedSkipsSessionsThatCannotStartAReview(t *testing.T) {
	reviewRun := domain.AutomationRunID("review-run")
	scheduledRun := domain.AutomationRunID("scheduled-run")
	ended := domain.SessionID("ended")
	for name, tc := range map[string]struct {
		session domain.SessionRecord
		want    bool
	}{
		"worker":                   {domain.SessionRecord{Kind: domain.KindWorker}, true},
		"started by a schedule":    {domain.SessionRecord{Kind: domain.KindWorker, AutomationRunID: &scheduledRun}, true},
		"started by a review":      {domain.SessionRecord{Kind: domain.KindWorker, AutomationRunID: &reviewRun}, false},
		"orchestrator":             {domain.SessionRecord{Kind: domain.KindOrchestrator}, false},
		"task preparation":         {domain.SessionRecord{Kind: domain.KindWorker, IsTaskPreparation: true}, false},
		"never finished launching": {domain.SessionRecord{Kind: domain.KindWorker, ProvisionState: domain.SessionProvisionFailed}, false},
	} {
		t.Run(name, func(t *testing.T) {
			store := sessionEndedStore()
			store.runs[reviewRun] = domain.AutomationRun{ID: reviewRun, AutomationID: "review", TriggerSessionID: &ended, Status: domain.AutomationRunRunning}
			store.runs[scheduledRun] = domain.AutomationRun{ID: scheduledRun, AutomationID: "nightly", Status: domain.AutomationRunRunning}
			tc.session.ID, tc.session.ProjectID = "worker-1", "app"
			store.sessions["worker-1"] = tc.session
			queued, err := New(Deps{Store: store}).SessionEnded(context.Background(), "worker-1")
			if err != nil || queued != tc.want {
				t.Fatalf("queued=%v err=%v, want %v", queued, err, tc.want)
			}
		})
	}
}

type transcriptSpawner struct {
	*recordingSpawner
	paths map[domain.SessionID]string
}

func (s transcriptSpawner) TranscriptPath(_ context.Context, id domain.SessionID) string {
	return s.paths[id]
}

// Reviews of different ended sessions must run side by side, and each review
// must be told which session ended, including where its transcript is, so the
// agent can find its work.
func TestTickLaunchesSessionEndedRunsWithEndedSessionContext(t *testing.T) {
	now := time.Date(2026, time.October, 13, 9, 0, 0, 0, time.UTC)
	store := sessionEndedStore()
	store.sessions["worker-1"] = domain.SessionRecord{
		ID: "worker-1", ProjectID: "app", Kind: domain.KindWorker, DisplayName: "Fix login redirect",
		Metadata: domain.SessionMetadata{Branch: "ao/fix-login", Prompt: "Fix the login\nredirect loop", NativeTranscriptPath: "/tmp/worker-1.jsonl"},
	}
	store.sessions["worker-2"] = domain.SessionRecord{ID: "worker-2", ProjectID: "app", Kind: domain.KindWorker}
	store.prs["worker-1"] = []domain.PullRequest{{URL: "https://github.com/acme/app/pull/7", Merged: true}}
	for i, ended := range []domain.SessionID{"worker-1", "worker-2"} {
		id := domain.AutomationRunID("run-" + string(ended))
		store.runs[id] = domain.AutomationRun{ID: id, AutomationID: "review", ScheduledFor: now.Add(time.Duration(i) * time.Millisecond), TriggerSessionID: &ended, Status: domain.AutomationRunPending}
	}
	spawner := transcriptSpawner{recordingSpawner: &recordingSpawner{store: store}, paths: map[domain.SessionID]string{"worker-2": "/home/dev/.claude/projects/app/worker-2.jsonl"}}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})

	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(spawner.calls) != 2 {
		t.Fatalf("spawns = %d, want both reviews at once", len(spawner.calls))
	}
	first := spawner.calls[0]
	if first.DisplayName != "Session review: Fix login redirect" {
		t.Fatalf("display name = %q", first.DisplayName)
	}
	for _, want := range []string{
		"Suggest improvements to our skills.",
		"- Session: worker-1",
		"- Branch: ao/fix-login",
		"- Pull request: https://github.com/acme/app/pull/7 (merged)",
		"- Task: Fix the login redirect loop",
		"- Transcript: /tmp/worker-1.jsonl",
		"ao session get worker-1",
	} {
		if !strings.Contains(first.Prompt, want) {
			t.Fatalf("prompt %q is missing %q", first.Prompt, want)
		}
	}
	if spawner.calls[1].DisplayName != "Session review: worker-2" {
		t.Fatalf("untitled session display name = %q", spawner.calls[1].DisplayName)
	}
	if !strings.Contains(spawner.calls[1].Prompt, "- Transcript: /home/dev/.claude/projects/app/worker-2.jsonl") {
		t.Fatalf("prompt %q is missing the located transcript", spawner.calls[1].Prompt)
	}
}

func TestSessionEndedTriggerValidation(t *testing.T) {
	now := time.Date(2026, time.October, 13, 9, 0, 0, 0, time.UTC)
	svc := New(Deps{Store: newFakeStore(), Clock: func() time.Time { return now }})
	ctx := context.Background()
	input := CreateInput{ProjectID: "scheduled", DisplayName: "Session review", Prompt: "Review it", Kind: domain.KindWorker, Timezone: "UTC"}
	wantCode := func(err error, code string) {
		t.Helper()
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != code {
			t.Fatalf("error = %v, want %s", err, code)
		}
	}

	input.Trigger = "webhook"
	_, err := svc.Create(ctx, input)
	wantCode(err, "INVALID_AUTOMATION_TRIGGER")
	input.Trigger, input.RRule = domain.AutomationTriggerSessionEnded, "FREQ=DAILY"
	_, err = svc.Create(ctx, input)
	wantCode(err, "INVALID_AUTOMATION_SCHEDULE")

	input.RRule = ""
	created, err := svc.Create(ctx, input)
	if err != nil || created.Trigger != domain.AutomationTriggerSessionEnded || created.RRuleText != "" {
		t.Fatalf("Create = %#v err=%v, want a session-ended automation without a rule", created, err)
	}
	schedule := domain.AutomationTriggerSchedule
	_, err = svc.Update(ctx, created.ID, UpdateInput{Trigger: &schedule})
	wantCode(err, "INVALID_AUTOMATION_SCHEDULE")
	rule := "FREQ=DAILY;BYHOUR=10;BYMINUTE=0;BYSECOND=0"
	updated, err := svc.Update(ctx, created.ID, UpdateInput{Trigger: &schedule, RRule: &rule})
	if err != nil || updated.Trigger != schedule || !updated.NextRunAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("Update to schedule = %#v err=%v, want next run at 10:00", updated, err)
	}
	sessionEnded := domain.AutomationTriggerSessionEnded
	updated, err = svc.Update(ctx, created.ID, UpdateInput{Trigger: &sessionEnded})
	if err != nil || updated.RRuleText != "" {
		t.Fatalf("Update to session ended = %#v err=%v, want the rule cleared", updated, err)
	}
}
