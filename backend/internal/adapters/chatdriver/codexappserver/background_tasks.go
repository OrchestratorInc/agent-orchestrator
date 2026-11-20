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

var (
	_ ports.ChatBackgroundTaskInspector    = (*conversation)(nil)
	_ ports.ChatBackgroundTaskStopper      = (*conversation)(nil)
	_ ports.ChatBackgroundTaskOutputReader = (*conversation)(nil)
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
			c.mu.Lock()
			started := c.backgroundCommands[task.ItemID].StartedAt
			c.mu.Unlock()
			out = append(out, ports.ChatBackgroundTask{ID: task.ProcessID, RunID: task.ItemID, Kind: "shell", Description: domain.SanitizeTaskDescription(task.Command), Command: domain.SanitizeTaskCommand(task.Command), State: "running", StartedAt: started})
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			// A listed terminal keeps its output however many foreground
			// commands print meanwhile; a quiet dev server is the usual case.
			runIDs := make([]string, 0, len(out))
			for _, task := range out {
				runIDs = append(runIDs, task.RunID)
				c.rememberBackgroundRun(task.ID, task.RunID)
			}
			c.backgroundOutput.Pin(runIDs)
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

// codexMaxOutputDeltas is how many output deltas the app-server streams for
// one command before it stops sending them (MAX_EXEC_OUTPUT_DELTAS_PER_CALL
// in codex-rs). The process keeps running; its newer output never arrives.
const codexMaxOutputDeltas = 10_000

// backgroundRunsKept bounds the terminals remembered after they leave the
// listing, so a failed task's last lines stay readable while buffered.
const backgroundRunsKept = 64

// BackgroundTaskOutput returns what a background terminal has printed since
// this controller attached. The app-server keeps streaming a backgrounded
// command's output on its exec item after the turn ends (codex-cli 0.158.0),
// but offers no read, so earlier output and output from before a reconnect
// are not available.
func (c *conversation) BackgroundTaskOutput(_ context.Context, taskID string) (ports.ChatBackgroundTaskOutput, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ports.ChatBackgroundTaskOutput{}, errConversationClosed
	}
	for _, task := range c.backgroundObserved.Snapshot(time.Now()) {
		if task.ID != taskID || task.Kind != "shell" || task.RunID == "" {
			continue
		}
		if output, ok := c.backgroundOutput.Read(task.RunID); ok {
			return output, nil
		}
		// A known terminal that has printed nothing yet.
		return ports.ChatBackgroundTaskOutput{}, nil
	}
	// A terminal that ended, or left the listing, stays readable while its
	// output is still buffered.
	c.mu.Lock()
	runID := c.backgroundRuns[taskID]
	c.mu.Unlock()
	if output, ok := c.backgroundOutput.Read(runID); runID != "" && ok {
		return output, nil
	}
	return ports.ChatBackgroundTaskOutput{}, ports.ErrBackgroundTaskOutputUnavailable
}

// rememberBackgroundRun records which exec item a terminal's output streams on.
func (c *conversation) rememberBackgroundRun(processID, runID string) {
	if processID == "" || runID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.backgroundRuns == nil {
		c.backgroundRuns = make(map[string]string)
	}
	if _, known := c.backgroundRuns[processID]; !known {
		c.backgroundRunOrder = append(c.backgroundRunOrder, processID)
	}
	c.backgroundRuns[processID] = runID
	for len(c.backgroundRunOrder) > backgroundRunsKept {
		delete(c.backgroundRuns, c.backgroundRunOrder[0])
		c.backgroundRunOrder = c.backgroundRunOrder[1:]
	}
}

func (c *conversation) observeBackgroundNotification(n notification) {
	if n.Method == codexproto.MethodItemCommandExecutionOutputDelta {
		var delta codexproto.CommandExecutionOutputDeltaNotification
		if json.Unmarshal(n.Params, &delta) == nil && (delta.ThreadID == "" || delta.ThreadID == c.threadID) {
			c.mu.Lock()
			_, started := c.backgroundCommands[delta.ItemID]
			c.mu.Unlock()
			if !started {
				// Attached after the command started, so its first output is gone.
				c.backgroundOutput.MarkDropped(delta.ItemID)
			}
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
			ID               string  `json:"id"`
			Type             string  `json:"type"`
			Tool             string  `json:"tool"`
			ProcessID        string  `json:"processId"`
			Command          string  `json:"command"`
			ExitCode         *int    `json:"exitCode"`
			AggregatedOutput *string `json:"aggregatedOutput"`
			AgentsStates     map[string]struct {
				Status string `json:"status"`
			} `json:"agentsStates"`
		} `json:"item"`
	}
	if json.Unmarshal(n.Params, &params) != nil || (params.ThreadID != "" && params.ThreadID != c.threadID) {
		return
	}
	now := time.Now()
	if params.Item.Type == "commandExecution" {
		c.mu.Lock()
		if n.Method == "item/started" {
			if c.backgroundCommands == nil {
				c.backgroundCommands = make(map[string]ports.ChatBackgroundTask)
			}
			if _, exists := c.backgroundCommands[params.Item.ID]; !exists {
				c.backgroundCommands[params.Item.ID] = ports.ChatBackgroundTask{StartedAt: &now, Description: params.Item.Command}
			}
			c.mu.Unlock()
			return
		}
		observed := c.backgroundCommands[params.Item.ID]
		delete(c.backgroundCommands, params.Item.ID)
		c.mu.Unlock()
		// A foreground completion has no background process handle.
		if params.Item.ProcessID == "" {
			return
		}
		c.rememberBackgroundRun(params.Item.ProcessID, params.Item.ID)
		if params.Item.AggregatedOutput != nil && *params.Item.AggregatedOutput != "" {
			// The whole output fills what the stream missed, such as lines after
			// the delta cap. Codex can still drop the first chunk from both.
			c.backgroundOutput.Replace(params.Item.ID, *params.Item.AggregatedOutput)
		}
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
		description := params.Item.Command
		if description == "" {
			description = observed.Description
		}
		c.backgroundObserved.Update(ports.ChatBackgroundTask{ID: params.Item.ProcessID, RunID: params.Item.ID, Kind: "shell", Description: description, State: state, StartedAt: observed.StartedAt}, now)
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
				c.backgroundObserved.Update(ports.ChatBackgroundTask{ID: "agent:" + id, RunID: runID, Kind: "agent", Description: "Subagent " + id, State: state, StartedAt: &now}, now)
			} else {
				c.backgroundObserved.Finish("agent:"+id, state, now)
			}
		}
	}
}
