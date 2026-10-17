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

func apiErrorCode(err error) string {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

// One click turns dreaming on, so an empty request must get the dreaming
// defaults; dreaming only works after a session ends, as a worker.
func TestCreateDreamingFillsTheDefaults(t *testing.T) {
	store := newFakeStore()
	svc := New(Deps{Store: store, NewID: func() string { return "1" }})
	rec, err := svc.Create(context.Background(), CreateInput{ProjectID: "scheduled", Purpose: domain.AutomationPurposeDreaming})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.DisplayName != "Dreaming" || rec.Prompt != DefaultDreamingPrompt || rec.Kind != domain.KindWorker ||
		rec.Trigger != domain.AutomationTriggerSessionEnded || rec.Purpose != domain.AutomationPurposeDreaming {
		t.Fatalf("dreaming automation = %#v", rec)
	}
	if len(DefaultDreamingPrompt) > maxPromptBytes {
		t.Fatalf("default prompt is %d bytes, over the %d-byte prompt limit", len(DefaultDreamingPrompt), maxPromptBytes)
	}
	for name, input := range map[string]CreateInput{
		"scheduled":    {Purpose: domain.AutomationPurposeDreaming, Trigger: domain.AutomationTriggerSchedule, RRule: "FREQ=DAILY"},
		"orchestrator": {Purpose: domain.AutomationPurposeDreaming, Kind: domain.KindOrchestrator},
		"unknown":      {Purpose: "nightmare", Kind: domain.KindWorker, DisplayName: "x", Prompt: "x"},
	} {
		input.ProjectID = "scheduled"
		if _, err := svc.Create(context.Background(), input); apiErrorCode(err) == "" {
			t.Fatalf("%s: Create error = %v, want a validation error", name, err)
		}
	}
	schedule := domain.AutomationTriggerSchedule
	rrule := "FREQ=DAILY;BYHOUR=9;BYMINUTE=0"
	if _, err := svc.Update(context.Background(), rec.ID, UpdateInput{Trigger: &schedule, RRule: &rrule}); apiErrorCode(err) != "INVALID_AUTOMATION_TRIGGER" {
		t.Fatalf("switching dreaming to a schedule err = %v", err)
	}
}

type duplicateDreamingStore struct{ *fakeStore }

func (duplicateDreamingStore) CreateAutomation(context.Context, domain.Automation) (domain.Automation, error) {
	return domain.Automation{}, domain.ErrDreamingAutomationExists
}

func TestCreateDreamingReportsAnExistingOneAsAConflict(t *testing.T) {
	svc := New(Deps{Store: duplicateDreamingStore{newFakeStore()}})
	if _, err := svc.Create(context.Background(), CreateInput{ProjectID: "scheduled", Purpose: domain.AutomationPurposeDreaming}); apiErrorCode(err) != "DREAMING_EXISTS" {
		t.Fatalf("second dreaming automation err = %v, want DREAMING_EXISTS", err)
	}
}

// dreamingStore adds what the user typed in each session: absent means the
// session had no chat conversation at all.
type dreamingStore struct {
	*schedulerMemoryStore
	typed map[domain.SessionID][]string
}

func (s dreamingStore) HumanMessagesForSession(_ context.Context, id domain.SessionID) ([]string, bool, error) {
	texts, found := s.typed[id]
	return texts, found, nil
}

func newDreamingStore() dreamingStore {
	store := sessionEndedStore()
	store.automations["dream"] = domain.Automation{
		ID: "dream", ProjectID: "app", DisplayName: "Dreaming", Prompt: DefaultDreamingPrompt, Kind: domain.KindWorker,
		Trigger: domain.AutomationTriggerSessionEnded, Purpose: domain.AutomationPurposeDreaming, Enabled: true,
	}
	store.projects["app"] = domain.ProjectRecord{ID: "app", DisplayName: "Acme app", Config: domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode}}}
	return dreamingStore{schedulerMemoryStore: store, typed: map[domain.SessionID][]string{}}
}

// A dream costs a session, so it must not start for a chat session where the
// user typed nothing after the opening task (which an orchestrator may have
// written); other session-ended automations still run.
func TestSessionEndedSkipsDreamingWhenTheUserTypedNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		typed     []string
		chat      bool
		wantDream bool
	}{
		"user typed":            {typed: []string{"Fix the bug.", "No, use unittest."}, chat: true, wantDream: true},
		"only the opening task": {typed: []string{"Fix the bug."}, chat: true},
		"user typed nothing":    {chat: true},
		"terminal session":      {wantDream: true},
	} {
		t.Run(name, func(t *testing.T) {
			store := newDreamingStore()
			store.sessions["worker-1"] = domain.SessionRecord{ID: "worker-1", ProjectID: "app", Kind: domain.KindWorker}
			if tc.chat {
				store.typed["worker-1"] = tc.typed
			}
			if _, err := New(Deps{Store: store}).SessionEnded(context.Background(), "worker-1"); err != nil {
				t.Fatalf("SessionEnded: %v", err)
			}
			queued := map[domain.AutomationID]bool{}
			for _, run := range store.runs {
				queued[run.AutomationID] = true
			}
			if queued["dream"] != tc.wantDream || !queued["review"] {
				t.Fatalf("queued = %v, want dream=%v and the review", queued, tc.wantDream)
			}
		})
	}
}

// A dream runs without a checkout, with the project's worker agent, sees only
// what the user typed, gets the exact commands to save instructions, and
// waits for the previous dream so two never edit at once.
func TestTickLaunchesOneDreamAtATimeAsAStandaloneSession(t *testing.T) {
	now := time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
	store := newDreamingStore()
	store.sessions["worker-1"] = domain.SessionRecord{ID: "worker-1", ProjectID: "app", Kind: domain.KindWorker, DisplayName: "Fix note ordering"}
	store.sessions["worker-2"] = domain.SessionRecord{ID: "worker-2", ProjectID: "app", Kind: domain.KindWorker}
	store.typed["worker-1"] = []string{"Fix the note ordering bug.", "No, run python3 -m unittest\ninstead of make test.", "Keep replies short."}
	for i, ended := range []domain.SessionID{"worker-1", "worker-2"} {
		id := domain.AutomationRunID("dream-" + string(ended))
		store.runs[id] = domain.AutomationRun{ID: id, AutomationID: "dream", ScheduledFor: now.Add(time.Duration(i) * time.Millisecond), TriggerSessionID: &ended, Status: domain.AutomationRunPending}
	}
	spawner := &recordingSpawner{store: store.schedulerMemoryStore}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }})
	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(spawner.calls) != 1 {
		t.Fatalf("spawns = %d, want one dream at a time", len(spawner.calls))
	}
	spawn := spawner.calls[0]
	if spawn.ProjectID != "" || spawn.Kind != domain.KindWorker || spawn.Harness != domain.HarnessClaudeCode || spawn.DisplayName != "Dreaming: Fix note ordering" {
		t.Fatalf("dream spawn = %#v, want a standalone worker on the project's agent", spawn)
	}
	for _, want := range []string{
		"You are AO's dreaming agent.",
		"- Session: worker-1",
		"What the user typed in that session after the opening task, oldest first:\n1. No, run python3 -m unittest instead of make test.\n2. Keep replies short.\nLearn only",
		"`ao instructions show --json`",
		"Project Acme app: add `--project app` to both commands.",
		"`ao session kill \"$AO_SESSION_ID\"`",
	} {
		if !strings.Contains(spawn.Prompt, want) {
			t.Fatalf("dream prompt missing %q:\n%s", want, spawn.Prompt)
		}
	}
}

// Without any agent to run, retrying can never succeed, so the run must fail
// with a message that says what to set.
func TestTickFailsADreamThatHasNoAgent(t *testing.T) {
	now := time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
	store := newDreamingStore()
	store.projects["app"] = domain.ProjectRecord{ID: "app"}
	ended := domain.SessionID("worker-1")
	store.runs["dream-1"] = domain.AutomationRun{ID: "dream-1", AutomationID: "dream", ScheduledFor: now, TriggerSessionID: &ended, Status: domain.AutomationRunPending}
	spawner := &recordingSpawner{store: store.schedulerMemoryStore}
	if err := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }}).Tick(context.Background(), now); err == nil {
		t.Fatalf("Tick succeeded without an agent")
	}
	if run := store.runs["dream-1"]; run.Status != domain.AutomationRunFailed || !strings.Contains(run.ErrorMessage, "dreaming needs an agent") || len(spawner.calls) != 0 {
		t.Fatalf("run = %#v, spawns = %d", run, len(spawner.calls))
	}
}
