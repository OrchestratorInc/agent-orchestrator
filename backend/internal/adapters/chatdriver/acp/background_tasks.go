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
			return tasks, err
		}
	}
	return c.backgroundObserved.Snapshot(time.Now()), ports.ErrBackgroundTasksPartial
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
	nativeID := stringField(output, "task_id", "taskId", "backgroundTaskId", "background_id", "sessionID", "sessionId", "session_id", "processId", "process_id", "shellId", "shell_id")
	if nativeID != "" && flagged {
		c.backgroundObserved.Remove(tool.id)
		id = nativeID
	}
	now := time.Now()
	if tool.status == acpsdk.ToolCallStatusPending {
		return
	}
	if !flagged {
		if referenced := stringField(input, "task_id", "taskId", "sessionID", "sessionId", "session_id"); referenced != "" {
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
	if nativeID != "" {
		switch reported := stringField(output, "state", "status"); reported {
		case "running", "paused", "completed", "failed", "stopped":
			state = reported
		}
	}

	description := stringField(input, "description", "command", "prompt")
	if description == "" {
		description = tool.title
	}
	c.backgroundObserved.Update(ports.ChatBackgroundTask{ID: id, Kind: kind, Description: description, State: state}, now)
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
		c.backgroundObserved.Update(ports.ChatBackgroundTask{ID: update.ID, Kind: update.TaskType, Description: description, State: "running"}, now)
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
