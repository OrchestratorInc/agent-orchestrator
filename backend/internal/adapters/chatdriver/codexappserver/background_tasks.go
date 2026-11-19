package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatBackgroundTaskInspector = (*conversation)(nil)

// BackgroundTasks reads the app-server's unified-exec registry, never the OS
// process tree. A page failure leaves the whole reading unavailable.
func (c *conversation) BackgroundTasks(ctx context.Context) ([]ports.ChatBackgroundTask, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errConversationClosed
	}
	out := make([]ports.ChatBackgroundTask, 0)
	seenIDs := make(map[string]bool)
	cursors := make(map[string]bool)
	cursor := ""
	for page := 0; page < 100; page++ {
		var response struct {
			Data []struct {
				ProcessID string `json:"processId"`
				ItemID    string `json:"itemId"`
				Command   string `json:"command"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		params := map[string]any{"threadId": c.threadID, "limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err := c.conn.request(ctx, "thread/backgroundTerminals/list", params, &response); err != nil {
			var rpcErr *rpcError
			if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
				if tasks := c.backgroundObserved.Snapshot(time.Now()); len(tasks) > 0 {
					return tasks, ports.ErrBackgroundTasksPartial
				}
				return nil, ports.ErrBackgroundTasksUnsupported
			}
			return nil, err
		}
		if response.Data == nil {
			return nil, errors.New("background-terminal response is missing data")
		}
		for _, task := range response.Data {
			if task.ProcessID == "" || seenIDs[task.ProcessID] {
				return nil, errors.New("background-terminal response has missing or duplicate identity")
			}
			seenIDs[task.ProcessID] = true
			c.rememberBackgroundRun(task.ProcessID, task.ItemID)
			command := domain.SanitizeControlChars(task.Command)
			out = append(out, ports.ChatBackgroundTask{ID: task.ProcessID, RunID: task.ItemID, Kind: "shell", Description: command, Command: command, State: "running"})
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			c.backgroundObserved.ReconcileShells(out, time.Now())
			tasks := c.backgroundObserved.Snapshot(time.Now())
			// Shell inventory is complete, but event-derived agents may predate
			// this controller attachment even when none have been observed yet.
			return tasks, ports.ErrBackgroundTasksPartial
		}
		cursor = *response.NextCursor
		if cursors[cursor] {
			return nil, fmt.Errorf("background-terminal cursor repeated")
		}
		cursors[cursor] = true
	}
	return nil, errors.New("background-terminal inventory exceeded 100 pages")
}

var _ ports.ChatBackgroundTaskOutputReader = (*conversation)(nil)

// BackgroundTaskOutput returns what a background terminal has printed since
// this controller attached. The app-server keeps streaming a backgrounded
// command's output on its exec item after the turn ends (codex-cli 0.158.0),
// but offers no read, so earlier output and output from before a reconnect
// are not available. A terminal stays readable after it leaves the inventory,
// so an open tab keeps a stopped terminal's output.
func (c *conversation) BackgroundTaskOutput(_ context.Context, taskID string) (ports.ChatBackgroundTaskOutput, error) {
	c.mu.Lock()
	closed := c.closed
	runID, known := c.backgroundRuns[taskID]
	c.mu.Unlock()
	if closed {
		return ports.ChatBackgroundTaskOutput{}, errConversationClosed
	}
	if !known {
		return ports.ChatBackgroundTaskOutput{}, ports.ErrBackgroundTaskOutputUnavailable
	}
	if output, ok := c.backgroundOutput.Read(runID); ok {
		return output, nil
	}
	// A known terminal that has printed nothing yet.
	return ports.ChatBackgroundTaskOutput{}, nil
}

// backgroundRunsKept bounds how many terminals keep their output key.
const backgroundRunsKept = 32

func (c *conversation) rememberBackgroundRun(processID, itemID string) {
	if itemID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.backgroundRuns == nil {
		c.backgroundRuns = make(map[string]string)
	}
	if _, ok := c.backgroundRuns[processID]; !ok {
		if len(c.backgroundRunOrder) >= backgroundRunsKept {
			delete(c.backgroundRuns, c.backgroundRunOrder[0])
			c.backgroundRunOrder = c.backgroundRunOrder[1:]
		}
		c.backgroundRunOrder = append(c.backgroundRunOrder, processID)
	}
	c.backgroundRuns[processID] = itemID
}

func (c *conversation) observeBackgroundNotification(n notification) {
	if n.Method == codexproto.MethodItemCommandExecutionOutputDelta {
		var delta codexproto.CommandExecutionOutputDeltaNotification
		if json.Unmarshal(n.Params, &delta) == nil && (delta.ThreadID == "" || delta.ThreadID == c.threadID) {
			c.backgroundOutput.Append(delta.ItemID, delta.Delta)
		}
		return
	}
	if n.Method != "item/started" && n.Method != "item/completed" {
		return
	}
	var params struct {
		ThreadID string `json:"threadId"`
		Item     struct {
			ID           string `json:"id"`
			Type         string `json:"type"`
			Tool         string `json:"tool"`
			ProcessID    string `json:"processId"`
			ExitCode     *int   `json:"exitCode"`
			AgentsStates map[string]struct {
				Status string `json:"status"`
			} `json:"agentsStates"`
		} `json:"item"`
	}
	if json.Unmarshal(n.Params, &params) != nil || (params.ThreadID != "" && params.ThreadID != c.threadID) {
		return
	}
	now := time.Now()
	if params.Item.Type == "commandExecution" && n.Method == "item/completed" {
		state := "completed"
		if params.Item.ExitCode != nil && *params.Item.ExitCode != 0 {
			state = "failed"
		}
		c.backgroundObserved.Finish(params.Item.ProcessID, state, now)
	}
	if params.Item.Type == "collabAgentToolCall" {
		// A child thread can execute more than once. Only calls that dispatch
		// work supply a new run identity; wait/status observations keep it.
		runID := ""
		switch params.Item.Tool {
		case "spawnAgent", "sendInput", "resumeAgent":
			runID = params.Item.ID
		}
		for id, agent := range params.Item.AgentsStates {
			var state string
			switch agent.Status {
			case "running", "pendingInit":
				state = "running"
			case "completed":
				state = "completed"
			case "errored":
				state = "failed"
			case "shutdown", "interrupted":
				state = "stopped"
			default:
				continue
			}
			if state == "running" || runID != "" {
				// Dispatch outcomes belong to their execution, not whichever
				// execution of this child happens to be current when replayed.
				c.backgroundObserved.Update(ports.ChatBackgroundTask{ID: "agent:" + id, RunID: runID, Kind: "agent", Description: "Subagent " + id, State: state}, now)
			} else {
				c.backgroundObserved.Finish("agent:"+id, state, now)
			}
		}
	}
}
