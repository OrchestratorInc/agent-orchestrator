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

// dreamingStore adds what the user typed in each session (absent means the
// session had no chat conversation at all), the commands its agent ran, newest
// first, and its turn states.
type dreamingStore struct {
	*schedulerMemoryStore
	typed    map[domain.SessionID][]string
	commands map[domain.SessionID][]domain.SessionCommand
	turns    map[domain.SessionID][]domain.TurnState
}

func (s dreamingStore) SessionTurnStates(_ context.Context, id domain.SessionID) ([]domain.TurnState, error) {
	return s.turns[id], nil
}

func (s dreamingStore) SessionCommands(_ context.Context, id domain.SessionID, limit int) ([]domain.SessionCommand, error) {
	return s.commands[id][:min(limit, len(s.commands[id]))], nil
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
	return dreamingStore{
		schedulerMemoryStore: store, typed: map[domain.SessionID][]string{},
		commands: map[domain.SessionID][]domain.SessionCommand{}, turns: map[domain.SessionID][]domain.TurnState{},
	}
}

// Every ended worker dreams, even one the user never typed in, such as a
// worker an orchestrator started: its commands can still teach the project
// facts.
func TestSessionEndedQueuesADreamWhenTheUserTypedNothing(t *testing.T) {
	store := newDreamingStore()
	store.sessions["worker-1"] = domain.SessionRecord{ID: "worker-1", ProjectID: "app", Kind: domain.KindWorker}
	store.typed["worker-1"] = []string{"Fix the bug."}
	if _, err := New(Deps{Store: store}).SessionEnded(context.Background(), "worker-1"); err != nil {
		t.Fatalf("SessionEnded: %v", err)
	}
	queued := map[domain.AutomationID]bool{}
	for _, run := range store.runs {
		queued[run.AutomationID] = true
	}
	if !queued["dream"] || !queued["review"] {
		t.Fatalf("queued = %v, want the dream and the review", queued)
	}
}

// What a dream may learn about the user depends on whether they typed after
// the opening task, which an orchestrator may have written; a terminal session
// has no chat record, so the dream reads its transcript.
func TestDreamMessagesSayWhatTheDreamMayLearnFrom(t *testing.T) {
	store := newDreamingStore()
	store.typed["typed"] = []string{"Fix the bug.", "No, use unittest."}
	store.typed["silent"] = []string{"Fix the bug."}
	for id, want := range map[domain.SessionID]string{
		"typed":    "What the user typed after it, oldest first:\n1. No, use unittest.\n",
		"silent":   "The user typed nothing after the opening task",
		"terminal": "AO has no chat record of that session.",
	} {
		if got := dreamMessages(context.Background(), store, id); !strings.Contains(got, want) {
			t.Fatalf("%s: messages = %q, want %q", id, got, want)
		}
	}
}

// A dream sees each command once with its latest result, oldest first, and
// never its output; without commands the section is left out.
func TestDreamCommandsListEachCommandOnceWithItsLatestResult(t *testing.T) {
	store := newDreamingStore()
	store.commands["worker-1"] = []domain.SessionCommand{
		{Command: "python3 -m unittest"},
		{Command: "make\n  test", Failed: true},
		{Command: "python3 -m unittest", Failed: true},
	}
	want := "Commands the agent ran, oldest first, with their latest result and without their output:\n- failed: make test\n- passed: python3 -m unittest"
	if got := dreamCommandsRun(context.Background(), store, "worker-1"); got != want {
		t.Fatalf("commands = %q, want %q", got, want)
	}
	if got := dreamCommandsRun(context.Background(), store, "worker-2"); got != "" {
		t.Fatalf("commands without any = %q, want none", got)
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
	store.commands["worker-1"] = []domain.SessionCommand{{Command: "python3 -m unittest"}, {Command: "make test", Failed: true}}
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
	// The mode is left to AO, so a terminal-only agent can dream too.
	if spawn.ProjectID != "" || spawn.Kind != domain.KindWorker || spawn.Harness != domain.HarnessClaudeCode || spawn.DisplayName != "Dreaming: Fix note ordering" || !spawn.Background || spawn.RequestedMode != "" {
		t.Fatalf("dream spawn = %#v, want a standalone background worker on the project's agent", spawn)
	}
	for _, want := range []string{
		"You are AO's dreaming agent.",
		"- Session: worker-1",
		"What the user typed after it, oldest first:\n1. No, run python3 -m unittest instead of make test.\n2. Keep replies short.\nLearn from these messages",
		"- failed: make test\n- passed: python3 -m unittest",
		"`ao instructions show --json`",
		"protectedLines (lines the user typed or edited)",
		"history (the newest edits",
		"Project Acme app: add `--project app` to both commands.",
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

// A background session that killed itself would cancel its last turn and
// look failed, so AO closes it once its turns settle: completed means the run
// completed, a failed last turn fails the run with a message, and an open turn
// (say, waiting for the user) leaves it running. A run that opened a PR stays
// open where the user sees it, and a visible session is the user's to close.
func TestTickClosesABackgroundRunOnceItsTurnsSettle(t *testing.T) {
	now := time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
	done := []domain.TurnState{domain.TurnStateCompleted}
	for name, tc := range map[string]struct {
		automation domain.AutomationID
		visible    bool
		withPR     bool
		turns      []domain.TurnState
		wantKilled bool
		wantStatus domain.AutomationRunStatus
	}{
		"dream completed":         {automation: "dream", turns: done, wantKilled: true, wantStatus: domain.AutomationRunCompleted},
		"dream failed":            {automation: "dream", turns: []domain.TurnState{domain.TurnStateCompleted, domain.TurnStateFailed}, wantKilled: true, wantStatus: domain.AutomationRunFailed},
		"dream still working":     {automation: "dream", turns: []domain.TurnState{domain.TurnStateCompleted, domain.TurnStateRunning}, wantStatus: domain.AutomationRunRunning},
		"dream not started yet":   {automation: "dream", wantStatus: domain.AutomationRunRunning},
		"review without a PR":     {automation: "review", turns: done, wantKilled: true, wantStatus: domain.AutomationRunCompleted},
		"review that opened a PR": {automation: "review", withPR: true, turns: done, wantStatus: domain.AutomationRunRunning},
		"visible session":         {automation: "review", visible: true, turns: done, wantStatus: domain.AutomationRunRunning},
	} {
		t.Run(name, func(t *testing.T) {
			store := newDreamingStore()
			ended, session := domain.SessionID("worker-1"), domain.SessionID("background-1")
			store.sessions[session] = domain.SessionRecord{ID: session, Kind: domain.KindWorker, Background: !tc.visible}
			store.runs["run-1"] = domain.AutomationRun{ID: "run-1", AutomationID: tc.automation, ScheduledFor: now, TriggerSessionID: &ended, SessionID: &session, Status: domain.AutomationRunRunning}
			store.turns[session] = tc.turns
			if tc.withPR {
				store.prs[session] = []domain.PullRequest{{URL: "https://github.com/acme/app/pull/8"}}
			}
			spawner := &recordingSpawner{store: store.schedulerMemoryStore}
			if err := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }}).Tick(context.Background(), now); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			run := store.runs["run-1"]
			if killed := len(spawner.kills) == 1 && spawner.kills[0] == session; killed != tc.wantKilled || run.Status != tc.wantStatus {
				t.Fatalf("killed=%v status=%s, want killed=%v status=%s", killed, run.Status, tc.wantKilled, tc.wantStatus)
			}
			if tc.wantStatus == domain.AutomationRunFailed && !strings.Contains(run.ErrorMessage, "Open it from the run history") {
				t.Fatalf("failed run message = %q", run.ErrorMessage)
			}
		})
	}
}
