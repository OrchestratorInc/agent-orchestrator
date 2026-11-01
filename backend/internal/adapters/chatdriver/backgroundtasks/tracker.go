// Package backgroundtasks keeps an event-derived view of explicitly backgrounded
// provider work. It never infers background execution from a long tool call,
// an idle turn, an OS process, or prose in a command's output.
package backgroundtasks

import (
	"sort"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Tracker is scoped to one native provider conversation. Its zero value is ready.
// Callers retain it across turns and reconstruct it from provider replay after
// reconnect. A read of this tracker alone is always a partial inventory.
type Tracker struct {
	mu    sync.Mutex
	tasks map[string]ports.ChatBackgroundTask
}

// Update records an explicit native task transition, deduplicating replay.
func (t *Tracker) Update(task ports.ChatBackgroundTask, now time.Time) {
	if task.ID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tasks == nil {
		t.tasks = make(map[string]ports.ChatBackgroundTask)
	}
	old, exists := t.tasks[task.ID]
	if exists && old.EndedAt != nil && (task.State == "running" || task.State == "paused") && (task.RunID == "" || task.RunID == old.RunID) {
		return
	}
	if exists && old.RunID != "" && task.RunID != "" && old.RunID != task.RunID && task.State != "running" {
		return
	}
	if task.Description == "" {
		task.Description = old.Description
	}
	task.Description = domain.SanitizeControlChars(task.Description)
	if task.Kind == "" {
		task.Kind = old.Kind
	}
	if task.Kind != "shell" && task.Kind != "agent" {
		task.Kind = "other"
	}
	if task.State == "" {
		task.State = "running"
	}
	if task.State != "running" && task.State != "paused" {
		if exists && old.State == task.State && old.EndedAt != nil {
			task.EndedAt = old.EndedAt
		} else {
			task.EndedAt = &now
		}
	}
	if task.State == "running" || task.State == "paused" {
		task.EndedAt = nil
	}
	t.tasks[task.ID] = task
	t.prune(now)
}

// Finish changes only a previously observed background task; ordinary foreground
// completions cannot introduce jobs into the panel.
func (t *Tracker) Finish(id, state string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	old, exists := t.tasks[id]
	if !exists {
		return
	}
	if state == old.State {
		return
	}
	old.State = state
	if state == "running" || state == "paused" {
		old.EndedAt = nil
	} else {
		old.EndedAt = &now
	}
	t.tasks[id] = old
	t.prune(now)
}

// Snapshot returns a copy of live jobs and outcomes from the last 30 seconds.
func (t *Tracker) Snapshot(now time.Time) []ports.ChatBackgroundTask {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(now)
	out := make([]ports.ChatBackgroundTask, 0, len(t.tasks))
	for _, task := range t.tasks {
		if task.EndedAt != nil {
			at := *task.EndedAt
			task.EndedAt = &at
		}
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (t *Tracker) prune(now time.Time) {
	for id, task := range t.tasks {
		if task.EndedAt != nil && now.Sub(*task.EndedAt) >= 30*time.Second {
			delete(t.tasks, id)
		}
	}
}

// Remove drops a provisional launch identity once a native job handle arrives.
func (t *Tracker) Remove(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tasks, id)
}

// ReconcileShells replaces the shell membership after a complete native read.
// Missing jobs are removed without inventing an outcome; native completion
// events retain real outcomes separately.
func (t *Tracker) ReconcileShells(tasks []ports.ChatBackgroundTask, now time.Time) {
	ids := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		ids[task.ID] = true
	}
	t.mu.Lock()
	for id, task := range t.tasks {
		if task.Kind == "shell" && task.State == "running" && !ids[id] {
			delete(t.tasks, id)
		}
	}
	t.mu.Unlock()
	for _, task := range tasks {
		t.Update(task, now)
	}
}
