package claudeacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/backgroundtasks"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func claudeBackgroundTasks(ctx context.Context, conn *acpsdk.ClientSideConnection, id acpsdk.SessionId) ([]ports.ChatBackgroundTask, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := conn.CallExtension(ctx, "_ao/session/background_tasks", map[string]any{"sessionId": id})
	if err != nil {
		var rpcErr *acpsdk.RequestError
		if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
			return nil, ports.ErrBackgroundTasksUnsupported
		}
		return nil, err
	}
	var result struct {
		Tasks []struct {
			ID          string     `json:"id"`
			RunID       string     `json:"runId"`
			Kind        string     `json:"kind"`
			Description string     `json:"description"`
			Command     string     `json:"command"`
			State       string     `json:"state"`
			StartedAt   *time.Time `json:"startedAt"`
			EndedAt     *time.Time `json:"endedAt"`
			Stoppable   bool       `json:"stoppable"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Tasks == nil {
		return nil, fmt.Errorf("background task response is missing tasks")
	}
	out := make([]ports.ChatBackgroundTask, 0, len(result.Tasks))
	seen := make(map[string]bool, len(result.Tasks))
	for _, task := range result.Tasks {
		if task.ID == "" || seen[task.ID] {
			return nil, fmt.Errorf("background task response has missing or duplicate identity")
		}
		seen[task.ID] = true
		switch task.Kind {
		case "shell", "agent", "other":
		default:
			task.Kind = "other"
		}
		switch task.State {
		case "running", "completed", "failed", "stopped":
		default:
			return nil, fmt.Errorf("background task response has invalid state %q", task.State)
		}
		description := domain.SanitizeControlChars(task.Description)
		out = append(out, ports.ChatBackgroundTask{
			ID: task.ID, RunID: task.RunID, Kind: task.Kind, Description: description,
			Command: domain.SanitizeControlChars(task.Command), State: task.State,
			StartedAt: task.StartedAt, EndedAt: task.EndedAt,
			// Only AO's patched runtime reports this, and only for a live task.
			Stoppable: task.Stoppable && task.State == "running",
		})
	}
	return out, nil
}

// claudeStopBackgroundTask stops one live task through AO's runtime extension,
// which calls the Claude SDK's stopTask. A missing method or a task that is no
// longer live is not stoppable rather than a provider failure.
func claudeStopBackgroundTask(ctx context.Context, conn *acpsdk.ClientSideConnection, id acpsdk.SessionId, taskID string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := conn.CallExtension(ctx, "_ao/session/stop_background_task", map[string]any{"sessionId": id, "taskId": taskID})
	var rpcErr *acpsdk.RequestError
	if errors.As(err, &rpcErr) && (rpcErr.Code == -32601 || rpcErr.Code == -32602) {
		return ports.ErrBackgroundTaskNotStoppable
	}
	return err
}

// claudeBackgroundTaskOutput reads the tail of the output file Claude Code
// named for a background shell, through AO's runtime extension. The runtime
// caps the read; this side keeps the last lines and strips terminal escapes.
func claudeBackgroundTaskOutput(ctx context.Context, conn *acpsdk.ClientSideConnection, id acpsdk.SessionId, taskID string) (ports.ChatBackgroundTaskOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := conn.CallExtension(ctx, "_ao/session/background_task_output", map[string]any{
		"sessionId": id, "taskId": taskID, "maxBytes": backgroundtasks.MaxOutputBytes,
	})
	if err != nil {
		var rpcErr *acpsdk.RequestError
		if errors.As(err, &rpcErr) && (rpcErr.Code == -32601 || rpcErr.Code == -32602) {
			return ports.ChatBackgroundTaskOutput{}, ports.ErrBackgroundTaskOutputUnavailable
		}
		return ports.ChatBackgroundTaskOutput{}, err
	}
	var result struct {
		Output    *string `json:"output"`
		Truncated bool    `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ports.ChatBackgroundTaskOutput{}, err
	}
	if result.Output == nil {
		return ports.ChatBackgroundTaskOutput{}, fmt.Errorf("background task output response is missing output")
	}
	return backgroundtasks.TailOutput(*result.Output, result.Truncated), nil
}
