package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.ChatBackgroundTaskInspector = (*conversation)(nil)
	_ ports.ChatBackgroundTaskStopper   = (*conversation)(nil)
)

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
			out = append(out, ports.ChatBackgroundTask{ID: task.ProcessID, RunID: task.ItemID, Kind: "shell", Description: domain.SanitizeControlChars(task.Command), State: "running"})
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			c.backgroundObserved.ReconcileShells(out, time.Now())
			tasks := c.backgroundObserved.Snapshot(time.Now())
			// Every supported app-server can terminate a listed terminal; an
			// agent or a settled shell has nothing to stop.
			for i := range tasks {
				tasks[i].Stoppable = seenIDs[tasks[i].ID] && tasks[i].State == "running"
			}
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

// StopBackgroundTask terminates one background terminal the app-server lists.
// Its subagents have no stop here.
func (c *conversation) StopBackgroundTask(ctx context.Context, taskID string) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ports.ErrBackgroundTaskNotStoppable
	}
	live := false
	for _, task := range c.backgroundObserved.Snapshot(time.Now()) {
		if task.ID == taskID && task.Kind == "shell" && task.State == "running" {
			live = true
			break
		}
	}
	if !live {
		return ports.ErrBackgroundTaskNotStoppable
	}
	// Marked before asking: the terminal's completion can arrive before the reply.
	c.setBackgroundStopping(taskID, true)
	var response struct {
		Terminated bool `json:"terminated"`
	}
	params := map[string]any{"threadId": c.threadID, "processId": taskID}
	if err := c.conn.request(ctx, "thread/backgroundTerminals/terminate", params, &response); err != nil {
		c.setBackgroundStopping(taskID, false)
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) && (rpcErr.Code == -32601 || rpcErr.Code == -32600 || rpcErr.Code == -32602) {
			return ports.ErrBackgroundTaskNotStoppable
		}
		return err
	}
	// False means the terminal had already exited.
	if !response.Terminated {
		c.setBackgroundStopping(taskID, false)
		return ports.ErrBackgroundTaskNotStoppable
	}
	c.backgroundObserved.Finish(taskID, "stopped", time.Now())
	return nil
}

func (c *conversation) setBackgroundStopping(processID string, stopping bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !stopping {
		delete(c.backgroundStopping, processID)
		return
	}
	if c.backgroundStopping == nil {
		c.backgroundStopping = make(map[string]bool)
	}
	c.backgroundStopping[processID] = true
}

func (c *conversation) observeBackgroundNotification(n notification) {
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
		c.mu.Lock()
		if c.backgroundStopping[params.Item.ProcessID] {
			// AO terminated it, so the signal's exit code is not a failure.
			state = "stopped"
			delete(c.backgroundStopping, params.Item.ProcessID)
		}
		c.mu.Unlock()
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
