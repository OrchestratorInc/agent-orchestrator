package backgroundtasks

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReplayCannotResurrectSettledTaskOrExtendItsRecentLifetime(t *testing.T) {
	var tracker Tracker
	now := time.Now()
	job := ports.ChatBackgroundTask{ID: "job", Kind: "shell", State: "running"}
	tracker.Update(job, now)
	tracker.Finish("job", "completed", now.Add(time.Second))
	tracker.Update(job, now.Add(2*time.Second))
	tracker.Finish("job", "completed", now.Add(20*time.Second))
	tasks := tracker.Snapshot(now.Add(29 * time.Second))
	if len(tasks) != 1 || tasks[0].State != "completed" || !tasks[0].EndedAt.Equal(now.Add(time.Second)) {
		t.Fatal(tasks)
	}
	if tasks = tracker.Snapshot(now.Add(31 * time.Second)); len(tasks) != 0 {
		t.Fatal(tasks)
	}
}

func TestReconcileRemovesMissingShellWithoutInventingOutcome(t *testing.T) {
	var tracker Tracker
	now := time.Now()
	tracker.Update(ports.ChatBackgroundTask{ID: "missing", Kind: "shell", State: "running"}, now)
	tracker.Update(ports.ChatBackgroundTask{ID: "agent", Kind: "agent", State: "running"}, now)
	tracker.Update(ports.ChatBackgroundTask{ID: "done", Kind: "shell", State: "running"}, now)
	tracker.Finish("done", "failed", now)
	tracker.ReconcileShells([]ports.ChatBackgroundTask{{ID: "live", Kind: "shell", State: "running"}}, now)
	tasks := tracker.Snapshot(now)
	if len(tasks) != 3 {
		t.Fatal(tasks)
	}
	for _, task := range tasks {
		if task.ID == "missing" {
			t.Fatal("invented a completion")
		}
	}
}

func TestStartTimeUsesExplicitObservationAndRestartsWithANewRun(t *testing.T) {
	var tracker Tracker
	now := time.Now()
	tracker.Update(ports.ChatBackgroundTask{ID: "job", RunID: "a", Kind: "shell", State: "running", StartedAt: &now}, now)
	// Polls and complete native reads repeat the task without a start time.
	tracker.ReconcileShells([]ports.ChatBackgroundTask{{ID: "job", RunID: "a", Kind: "shell", State: "running"}}, now.Add(5*time.Second))
	tasks := tracker.Snapshot(now.Add(5 * time.Second))
	if len(tasks) != 1 || tasks[0].StartedAt == nil || !tasks[0].StartedAt.Equal(now) {
		t.Fatalf("start time moved on a repeat reading: %+v", tasks)
	}
	next := now.Add(9 * time.Second)
	tracker.Update(ports.ChatBackgroundTask{ID: "job", RunID: "b", Kind: "shell", State: "running", StartedAt: &next}, next)
	tasks = tracker.Snapshot(now.Add(9 * time.Second))
	if len(tasks) != 1 || !tasks[0].StartedAt.Equal(now.Add(9*time.Second)) {
		t.Fatalf("a new run kept the old start time: %+v", tasks)
	}
	// Snapshots are copies: changing one cannot move the tracker's time.
	*tasks[0].StartedAt = now.Add(time.Hour)
	if again := tracker.Snapshot(now.Add(9 * time.Second)); !again[0].StartedAt.Equal(now.Add(9 * time.Second)) {
		t.Fatal("snapshot shared the tracker's start time")
	}
}

func TestFinishCannotResurrectTerminalTask(t *testing.T) {
	var tracker Tracker
	now := time.Now()
	tracker.Update(ports.ChatBackgroundTask{ID: "job", State: "paused"}, now)
	tracker.Finish("job", "running", now.Add(time.Second))
	if tasks := tracker.Snapshot(now); len(tasks) != 1 || tasks[0].State != "running" || tasks[0].EndedAt != nil {
		t.Fatalf("paused task did not resume: %+v", tasks)
	}
	tracker.Finish("job", "completed", now.Add(2*time.Second))
	for _, state := range []string{"running", "paused"} {
		tracker.Finish("job", state, now.Add(3*time.Second))
		if tasks := tracker.Snapshot(now); len(tasks) != 1 || tasks[0].State != "completed" || !tasks[0].EndedAt.Equal(now.Add(2*time.Second)) {
			t.Fatalf("terminal task resumed: %+v", tasks)
		}
	}
}

func TestSettledReplayProtectionOutlivesVisibleHistory(t *testing.T) {
	for _, runID := range []string{"", "first"} {
		t.Run(runID, func(t *testing.T) {
			var tracker Tracker
			now := time.Now()
			job := ports.ChatBackgroundTask{ID: "job", RunID: runID, State: "running"}
			tracker.Update(job, now)
			tracker.Finish("job", "completed", now)
			later := now.Add(31 * time.Second)
			if tasks := tracker.Snapshot(later); len(tasks) != 0 {
				t.Fatal(tasks)
			}
			tracker.Update(job, later)
			if tasks := tracker.Snapshot(later); len(tasks) != 0 {
				t.Fatalf("expired run resurrected: %+v", tasks)
			}
			tracker.Update(ports.ChatBackgroundTask{ID: "job", RunID: "second", State: "running"}, later)
			tracker.Update(job, later)
			if tasks := tracker.Snapshot(later); len(tasks) != 1 || tasks[0].RunID != "second" || tasks[0].State != "running" {
				t.Fatalf("new run was lost: %+v", tasks)
			}
			tracker.Finish("job", "completed", later)
			later = later.Add(31 * time.Second)
			tracker.Snapshot(later)
			tracker.Update(job, later)
			tracker.Update(ports.ChatBackgroundTask{ID: "job", RunID: "second", State: "running"}, later)
			if tasks := tracker.Snapshot(later); len(tasks) != 0 {
				t.Fatalf("an older settled run resurrected: %+v", tasks)
			}
		})
	}
}

func TestActiveIgnoresTasksWithNoRecentEvent(t *testing.T) {
	var tracker Tracker
	now := time.Now()
	tracker.Update(ports.ChatBackgroundTask{ID: "job", Kind: "shell", State: "running"}, now)
	if !tracker.Active(now.Add(29*time.Minute), 30*time.Minute) {
		t.Fatal("a recently reported task stopped counting as active")
	}
	// A completion that never arrives must not hold the provider awake forever.
	if tracker.Active(now.Add(30*time.Minute), 30*time.Minute) {
		t.Fatal("a task with no event for 30 minutes still counts as active")
	}
	tracker.Finish("job", "paused", now.Add(40*time.Minute))
	if !tracker.Active(now.Add(41*time.Minute), 30*time.Minute) {
		t.Fatal("a new event did not refresh the task")
	}
}

func TestInventoryDoesNotInventStartTime(t *testing.T) {
	var tracker Tracker
	tracker.ReconcileShells([]ports.ChatBackgroundTask{{ID: "late", Kind: "shell", State: "running"}}, time.Now())
	if tasks := tracker.Snapshot(time.Now()); len(tasks) != 1 || tasks[0].StartedAt != nil {
		t.Fatal(tasks)
	}
}

func TestPersistedStartedAtSurvivesTrackerRebuild(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStore(dir)
	sessionID := "ses-1"
	t1 := Tracker{SessionID: sessionID, Store: store}

	startTime := time.Now().Add(-10 * time.Minute).Truncate(time.Millisecond).UTC()
	t1.Update(ports.ChatBackgroundTask{
		ID:        "task-1",
		RunID:     "run-1",
		Kind:      "shell",
		State:     "running",
		StartedAt: &startTime,
	}, startTime)

	// Rebuild tracker simulating daemon restart or chat reconnect with fresh in-memory state.
	t2 := Tracker{SessionID: sessionID, Store: NewFileStore(dir)}
	// Later reading or replay with newer timestamp must keep the persisted start time.
	now := time.Now().Truncate(time.Millisecond).UTC()
	t2.Update(ports.ChatBackgroundTask{
		ID:        "task-1",
		RunID:     "run-1",
		Kind:      "shell",
		State:     "running",
		StartedAt: &now,
	}, now)

	tasks := t2.Snapshot(now)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].StartedAt == nil || !tasks[0].StartedAt.Equal(startTime) {
		t.Fatalf("StartedAt was not restored from store: got %v, want %v", tasks[0].StartedAt, startTime)
	}

	// Also verify ReconcileShells without a start time restores persisted start time.
	t3 := Tracker{SessionID: sessionID, Store: NewFileStore(dir)}
	t3.ReconcileShells([]ports.ChatBackgroundTask{{
		ID:    "task-1",
		RunID: "run-1",
		Kind:  "shell",
		State: "running",
	}}, now)
	tasks = t3.Snapshot(now)
	if len(tasks) != 1 || tasks[0].StartedAt == nil || !tasks[0].StartedAt.Equal(startTime) {
		t.Fatalf("ReconcileShells did not restore persisted StartedAt: %+v", tasks)
	}
}

func TestNewRunIDResetsElapsedTime(t *testing.T) {
	store := NewMemoryStore()
	sessionID := "ses-1"
	tracker := Tracker{SessionID: sessionID, Store: store}

	startTime1 := time.Now().Add(-5 * time.Minute).Truncate(time.Millisecond).UTC()
	tracker.Update(ports.ChatBackgroundTask{
		ID:        "task-1",
		RunID:     "run-1",
		Kind:      "shell",
		State:     "running",
		StartedAt: &startTime1,
	}, startTime1)

	tasks := tracker.Snapshot(startTime1)
	if len(tasks) != 1 || tasks[0].StartedAt == nil || !tasks[0].StartedAt.Equal(startTime1) {
		t.Fatalf("run-1 start time incorrect: %+v", tasks)
	}

	// New run of the same task ID starts from zero (its own new start time).
	startTime2 := time.Now().Truncate(time.Millisecond).UTC()
	tracker.Update(ports.ChatBackgroundTask{
		ID:        "task-1",
		RunID:     "run-2",
		Kind:      "shell",
		State:     "running",
		StartedAt: &startTime2,
	}, startTime2)

	tasks = tracker.Snapshot(startTime2)
	if len(tasks) != 1 || tasks[0].StartedAt == nil || !tasks[0].StartedAt.Equal(startTime2) {
		t.Fatalf("run-2 did not reset start time: got %v, want %v", tasks[0].StartedAt, startTime2)
	}
}

func TestStartedAtStaysUnsetWhenNoStartTimeAvailable(t *testing.T) {
	store := NewMemoryStore()
	sessionID := "ses-1"
	tracker := Tracker{SessionID: sessionID, Store: store}

	now := time.Now()
	tracker.Update(ports.ChatBackgroundTask{
		ID:    "unknown-start",
		RunID: "run-x",
		Kind:  "shell",
		State: "running",
	}, now)

	tasks := tracker.Snapshot(now)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].StartedAt != nil {
		t.Fatalf("expected StartedAt to stay nil, got %v", tasks[0].StartedAt)
	}
}
