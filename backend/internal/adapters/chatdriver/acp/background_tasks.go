package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// BackgroundTasks prefers a provider's native inventory. Every ACP binding also
// tracks explicitly backgrounded tools, without requiring a provider-specific
// callback. That fallback is partial: ACP v1 cannot enumerate all native jobs.
func (c *conversation) BackgroundTasks(ctx context.Context) ([]ports.ChatBackgroundTask, error) {
	c.mu.Lock()
	id, closed := c.sessionID, c.closed
	c.mu.Unlock()
	if closed {
		return nil, errConversationClosed
	}
	if c.backgroundTasks != nil {
		tasks, err := c.backgroundTasks(ctx, c.conn, acpsdk.SessionId(id))
		if !errors.Is(err, ports.ErrBackgroundTasksUnsupported) {
			if err == nil && c.backgroundObserved.Store != nil {
				for i := range tasks {
					if tasks[i].ID == "" {
						continue
					}
					runID := tasks[i].RunID
					if runID == "" {
						if observed, ok := c.backgroundObserved.Get(tasks[i].ID); ok && observed.RunID != "" {
							runID = observed.RunID
							tasks[i].RunID = runID
						}
					}
					stored, ok := c.backgroundObserved.Store.GetStartedAt(c.backgroundObserved.SessionID, tasks[i].ID, runID)
					if !ok && runID != "" {
						if observed, okObs := c.backgroundObserved.Get(tasks[i].ID); okObs && observed.RunID != "" && observed.RunID != runID {
							stored, ok = c.backgroundObserved.Store.GetStartedAt(c.backgroundObserved.SessionID, tasks[i].ID, observed.RunID)
						}
						if !ok {
							stored, ok = c.backgroundObserved.Store.GetStartedAt(c.backgroundObserved.SessionID, tasks[i].ID, "")
						}
					}
					if ok && !stored.IsZero() {
						if tasks[i].StartedAt == nil || stored.Before(*tasks[i].StartedAt) {
							tasks[i].StartedAt = &stored
						} else if tasks[i].StartedAt != nil && tasks[i].StartedAt.Before(stored) {
							if err := c.backgroundObserved.Store.SetStartedAt(c.backgroundObserved.SessionID, tasks[i].ID, runID, *tasks[i].StartedAt); err != nil {
								c.log.Error("persist native task start time failed", "sessionID", c.backgroundObserved.SessionID, "taskID", tasks[i].ID, "runID", runID, "error", err)
							}
						}
					} else if tasks[i].StartedAt != nil && !tasks[i].StartedAt.IsZero() {
						if err := c.backgroundObserved.Store.SetStartedAt(c.backgroundObserved.SessionID, tasks[i].ID, runID, *tasks[i].StartedAt); err != nil {
							c.log.Error("persist native task start time failed", "sessionID", c.backgroundObserved.SessionID, "taskID", tasks[i].ID, "runID", runID, "error", err)
						}
					}
				}
			}
			return tasks, err
		}
	}
	return c.backgroundObserved.Snapshot(time.Now()), ports.ErrBackgroundTasksPartial
}

var _ ports.ChatBackgroundTaskStopper = (*conversation)(nil)

// StopBackgroundTask asks the provider to stop one of its own background tasks.
func (c *conversation) StopBackgroundTask(ctx context.Context, taskID string) error {
	c.mu.Lock()
	id, closed := c.sessionID, c.closed
	c.mu.Unlock()
	if closed {
		return errConversationClosed
	}
	if c.stopBackgroundTask == nil || taskID == "" {
		return ports.ErrBackgroundTaskNotStoppable
	}
	return c.stopBackgroundTask(ctx, c.conn, acpsdk.SessionId(id), taskID)
}

var _ ports.ChatBackgroundTaskOutputReader = (*conversation)(nil)

// BackgroundTaskOutput reads one background task's recent output from the
// provider. Tasks known only from tool events have no output source.
func (c *conversation) BackgroundTaskOutput(ctx context.Context, taskID string) (ports.ChatBackgroundTaskOutput, error) {
	c.mu.Lock()
	id, closed := c.sessionID, c.closed
	c.mu.Unlock()
	if closed {
		return ports.ChatBackgroundTaskOutput{}, errConversationClosed
	}
	if c.backgroundTaskOutput == nil || taskID == "" {
		return ports.ChatBackgroundTaskOutput{}, ports.ErrBackgroundTaskOutputUnavailable
	}
	return c.backgroundTaskOutput(ctx, c.conn, acpsdk.SessionId(id), taskID)
}

func (c *conversation) observeBackgroundTool(tool *toolState) {
	// A provider can report an explicit background transition in raw input or
	// metadata. Do not identify jobs by titles, output text, or terminal existence.
	input := backgroundObject(tool.rawInput)
	output := backgroundObject(tool.rawOutput)
	// Native ACP results may wrap their machine payload in an output field.
	if nested := backgroundObject(output["output"]); nested != nil {
		output = nested
	}
	air := nestedMap(nestedMap(tool.meta, "jetbrains"), "air")
	async := nestedMap(air, "asyncTasks")
	flagged := backgroundFlag(input) || backgroundFlag(output) || backgroundFlag(tool.meta) || backgroundFlag(async)
	id := tool.id
	// Background result handles outlive the launching tool call. A completed
	// launch is not a completed background job.
	nativeID := backgroundTaskID(output)
	if nativeID != "" && flagged {
		c.backgroundObserved.Remove(tool.id)
		id = nativeID
	}
	now := time.Now()
	if tool.status == acpsdk.ToolCallStatusPending {
		return
	}
	if !flagged {
		if referenced := c.backgroundTaskReference(input, now); referenced != "" {
			switch state := stringField(output, "state", "status"); state {
			case "completed", "failed", "stopped":
				c.backgroundObserved.Finish(referenced, state, now)
			}
		}
		if toolTerminal(tool.status) {
			c.backgroundObserved.Finish(id, backgroundToolState(tool.status), now)
		}
		return
	}
	kind := "other"
	if tool.kind == acpsdk.ToolKindExecute {
		kind = "shell"
	}
	name := stringField(nestedMap(tool.meta, "claudeCode"), "toolName")
	if name == "Agent" || name == "Task" || stringField(input, "subagent_type", "agent", "agentId") != "" {
		kind = "agent"
	}
	state := "running"
	if tool.status == acpsdk.ToolCallStatusFailed {
		state = "failed"
	}
	// Explicit job state applies to tool-ID tasks too. The ACP tool status
	// alone still cannot settle an independently running background launch.
	switch reported := stringField(output, "state", "status"); reported {
	case "running", "paused", "completed", "failed", "stopped":
		state = reported
	default:
		// With no independent handle or reported job state, the tool call is
		// the only lifecycle we can track. Do not keep its input flag alive
		// after the call settles.
		if nativeID == "" && toolTerminal(tool.status) {
			state = backgroundToolState(tool.status)
		}
	}

	description := stringField(input, "description", "command", "prompt")
	if description == "" {
		description = tool.title
	}
	command := ""
	if kind == "shell" {
		command = stringField(input, "command", "cmd")
	}
	var startPtr *time.Time
	if !tool.startedAt.IsZero() {
		startPtr = &tool.startedAt
	}
	c.backgroundObserved.Update(ports.ChatBackgroundTask{ID: id, RunID: tool.id, Kind: kind, Description: description, Command: command, State: state, StartedAt: startPtr}, now)
	if kind == "agent" {
		if session := stringField(output, "sessionId", "sessionID", "session_id"); session != "" {
			c.mu.Lock()
			if c.backgroundAgentSessions == nil {
				c.backgroundAgentSessions = make(map[string]string)
			}
			c.backgroundAgentSessions[id] = session
			c.mu.Unlock()
		}
	}
	// Prune aliases with the live task view, rather than retaining old sessions.
	c.backgroundTaskReference(nil, now)
}

// Session IDs are reference-only aliases for already observed agents. They
// never identify a new job, and ambiguous shared sessions cannot settle either job.
func (c *conversation) backgroundTaskReference(input map[string]any, now time.Time) string {
	nativeID := backgroundTaskID(input)
	session := stringField(input, "sessionId", "sessionID", "session_id")
	tasks := c.backgroundObserved.Snapshot(now)
	live := make(map[string]bool)
	for _, task := range tasks {
		if task.Kind == "agent" && (task.State == "running" || task.State == "paused") {
			live[task.ID] = true
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	match := ""
	ambiguous := false
	for id, alias := range c.backgroundAgentSessions {
		if !live[id] {
			delete(c.backgroundAgentSessions, id)
			continue
		}
		if session != "" && alias == session {
			if match != "" {
				ambiguous = true
			}
			match = id
		}
	}
	if nativeID != "" {
		return nativeID
	}
	if ambiguous {
		return ""
	}
	return match
}

func backgroundTaskID(value map[string]any) string {
	return stringField(value, "task_id", "taskId", "backgroundTaskId", "background_id", "processId", "process_id", "shellId", "shell_id")
}

func backgroundFlag(value map[string]any) bool {
	if mode, _ := value["mode"].(string); mode == "async" {
		return true
	}
	for _, key := range []string{"run_in_background", "runInBackground", "background", "backgrounded", "isBackground", "fireAndForget", "fire_and_forget", "async"} {
		if flag, _ := value[key].(bool); flag {
			return true
		}
	}
	return false
}

func stringField(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, _ := value[key].(string); text != "" {
			return text
		}
	}
	return ""
}

func backgroundToolState(status acpsdk.ToolCallStatus) string {
	if status == acpsdk.ToolCallStatusFailed {
		return "failed"
	}
	return "completed"
}

// observeAsyncTaskNotification handles the published ACP v1 asyncTasks extension
// before the pinned SDK rejects an unknown sessionUpdate discriminator.
func (c *conversation) observeAsyncTaskNotification(raw json.RawMessage) bool {
	var notification struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			Type        string `json:"sessionUpdate"`
			ID          string `json:"asyncTaskId"`
			ToolCallID  string `json:"toolCallId"`
			Name        string `json:"name"`
			Description string `json:"description"`
			TaskType    string `json:"taskType"`
			State       string `json:"state"`
		} `json:"update"`
	}
	if json.Unmarshal(raw, &notification) != nil {
		return false
	}
	update := notification.Update
	kind := strings.TrimPrefix(update.Type, "_")
	if kind != "async_task_spawned" && kind != "async_task_state_update" && kind != "async_task_progress" {
		return false
	}
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	if update.ID == "" || (sessionID != "" && notification.SessionID != sessionID) {
		return true
	}
	now := time.Now()
	switch kind {
	case "async_task_spawned":
		c.backgroundObserved.Remove(update.ToolCallID)
		description := update.Description
		if description == "" {
			description = update.Name
		}
		var startPtr *time.Time
		if !c.historyReplayActive() {
			startPtr = &now
		}
		c.backgroundObserved.Update(ports.ChatBackgroundTask{ID: update.ID, RunID: update.ToolCallID, Kind: update.TaskType, Description: description, State: "running", StartedAt: startPtr}, now)
	case "async_task_state_update":
		switch update.State {
		case "running", "paused", "completed", "failed", "stopped":
			c.backgroundObserved.Finish(update.ID, update.State, now)
		}
	}
	return true
}

// Some native ACP adapters encode their machine result as a JSON string in
// rawOutput. Decode only JSON objects; never parse task IDs out of output prose.
func backgroundObject(raw any) map[string]any {
	if object, ok := raw.(map[string]any); ok {
		return object
	}
	var encoded []byte
	switch value := raw.(type) {
	case string:
		encoded = []byte(value)
	case json.RawMessage:
		encoded = value
	default:
		return nil
	}
	var object map[string]any
	if json.Unmarshal(encoded, &object) != nil {
		return nil
	}
	return object
}
