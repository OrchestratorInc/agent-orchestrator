package claudeacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

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
			State       string     `json:"state"`
			EndedAt     *time.Time `json:"endedAt"`
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
		out = append(out, ports.ChatBackgroundTask{ID: task.ID, RunID: task.RunID, Kind: task.Kind, Description: description, State: task.State, EndedAt: task.EndedAt})
	}
	return out, nil
}
