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
