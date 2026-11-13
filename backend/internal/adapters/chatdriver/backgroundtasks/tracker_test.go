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

func TestStartTimeIsFirstObservationAndRestartsWithANewRun(t *testing.T) {
	var tracker Tracker
	now := time.Now()
	tracker.Update(ports.ChatBackgroundTask{ID: "job", RunID: "a", Kind: "shell", State: "running"}, now)
	// Polls and complete native reads repeat the task without a start time.
	tracker.ReconcileShells([]ports.ChatBackgroundTask{{ID: "job", RunID: "a", Kind: "shell", State: "running"}}, now.Add(5*time.Second))
	tasks := tracker.Snapshot(now.Add(5 * time.Second))
	if len(tasks) != 1 || tasks[0].StartedAt == nil || !tasks[0].StartedAt.Equal(now) {
		t.Fatalf("start time moved on a repeat reading: %+v", tasks)
	}
	tracker.Update(ports.ChatBackgroundTask{ID: "job", RunID: "b", Kind: "shell", State: "running"}, now.Add(9*time.Second))
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
