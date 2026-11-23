package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

// Unconfirmed foreground starts are bounded; listed jobs keep their metadata
// until completion so a late outcome survives inventory reconciliation.
const maxPendingBackgroundCommands = 256

type backgroundStart struct{ printed bool }

type backgroundCommand struct {
	StartedAt       *time.Time
	Description     string
	TurnID          string
	CallCompletedAt time.Time
	Listed          bool
}

// BackgroundTasks reads the app-server's unified-exec registry, never the OS
// process tree. The registry also contains foreground calls, so require a read
// begun after the launching call or turn finished. A page failure leaves the
// whole reading unavailable.
func (c *conversation) BackgroundTasks(ctx context.Context) ([]ports.ChatBackgroundTask, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c.mu.Lock()
	closed := c.closed
	needHistory := c.backgroundHistoryPending
	c.mu.Unlock()
	if closed {
		return nil, errConversationClosed
	}
	var recoveryTurns []string
	recoveryIdle := false
	if needHistory {
		history, err := c.readBackgroundLaunchHistory(ctx)
		if err != nil {
			return nil, err
		}
		c.rememberBackgroundLaunchHistory(history.Turns)
		recoveryTurns, recoveryIdle = idleBackgroundTurns(history)
	}
	c.mu.Lock()
	closed = c.closed
	// Completion observed during this RPC cannot qualify its earlier snapshot.
	eligible := make(map[string]bool)
	for id, command := range c.backgroundCommands {
		eligible[id] = !command.CallCompletedAt.IsZero()
	}
	c.mu.Unlock()
	if closed {
		return nil, errConversationClosed
	}
	type terminal struct {
		ProcessID string `json:"processId"`
		ItemID    string `json:"itemId"`
		Command   string `json:"command"`
	}
	var inventory []terminal
	prior := make(map[string]ports.ChatBackgroundTask)
	for _, task := range c.backgroundObserved.Snapshot(time.Now()) {
		if task.Kind == "shell" && task.State == "running" {
			prior[task.ID] = task
		}
	}
	out := make([]ports.ChatBackgroundTask, 0)
	seenIDs := make(map[string]bool)
	confirmedIDs := make(map[string]bool)
	cursors := make(map[string]bool)
	cursor := ""
	for page := 0; page < 100; page++ {
		var response struct {
			Data       []terminal `json:"data"`
			NextCursor *string    `json:"nextCursor"`
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
			inventory = append(inventory, task)
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			recoverUnmatched := false
			if recoveryIdle {
				after, err := c.readBackgroundLaunchHistory(ctx)
				if err != nil {
					return nil, err
				}
				afterTurns, idle := idleBackgroundTurns(after)
				recoverUnmatched = idle && slices.Equal(recoveryTurns, afterTurns)
			}
			c.mu.Lock()
			if recoverUnmatched {
				c.backgroundHistoryPending = false
			}
			for _, task := range inventory {
				observed, known := c.backgroundCommands[task.ItemID]
				previous, confirmed := prior[task.ProcessID]
				confirmed = confirmed && previous.RunID == task.ItemID
				launchCompleted := known && eligible[task.ItemID] && !observed.CallCompletedAt.IsZero()
				if !launchCompleted && !confirmed && !recoverUnmatched {
					continue
				}
				if observed.StartedAt == nil && confirmed {
					observed.StartedAt = previous.StartedAt
				}
				if observed.CallCompletedAt.IsZero() {
					observed.CallCompletedAt = time.Now()
				}
				observed.Listed = true
				observed.Description = domain.SanitizeTaskDescription(task.Command)
				c.rememberBackgroundCommand(task.ItemID, observed)
				confirmedIDs[task.ProcessID] = true
				out = append(out, ports.ChatBackgroundTask{ID: task.ProcessID, RunID: task.ItemID, Kind: "shell", Description: observed.Description, Command: domain.SanitizeTaskCommand(task.Command), State: "running", StartedAt: observed.StartedAt})
			}
			c.mu.Unlock()
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
				tasks[i].Stoppable = confirmedIDs[tasks[i].ID] && tasks[i].State == "running"
			}
			// Launch evidence and event-derived agents may predate attachment;
			// unknown shells need a launch boundary or a stable idle bracket.
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

// StopBackgroundTask terminates one confirmed background terminal.
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

// rememberBackgroundCommand is called with mu held. Unconfirmed metadata is
// bounded, including history and launch completions whose start was not observed.
func (c *conversation) rememberBackgroundCommand(id string, command backgroundCommand) {
	if id == "" {
		return
	}
	if c.backgroundCommands == nil {
		c.backgroundCommands = make(map[string]backgroundCommand)
	}
	if _, exists := c.backgroundCommands[id]; !exists && !command.Listed {
		pending, oldestID := 0, ""
		var oldest time.Time
		for candidateID, candidate := range c.backgroundCommands {
			if candidate.Listed {
				continue
			}
			at := candidate.CallCompletedAt
			if candidate.StartedAt != nil {
				at = *candidate.StartedAt
			}
			pending++
			if oldestID == "" || at.Before(oldest) {
				oldestID, oldest = candidateID, at
			}
		}
		if pending >= maxPendingBackgroundCommands {
			delete(c.backgroundCommands, oldestID)
		}
	}
	c.backgroundCommands[id] = command
}

// codexMaxOutputDeltas is how many output deltas the app-server streams for
// one command before it stops sending them (MAX_EXEC_OUTPUT_DELTAS_PER_CALL
// in codex-rs). The process keeps running; its newer output never arrives.
const codexMaxOutputDeltas = 10_000

// backgroundRunsKept bounds the terminals remembered after they leave the
// listing, so a failed task's last lines stay readable while buffered.
const backgroundRunsKept = 64

// backgroundStartsKept bounds the command starts remembered. Foreground
// commands start exec items too, so it is larger than the output buffer.
const backgroundStartsKept = 512

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
		// A terminal started before this controller attached may have printed
		// before then, and one that printed has lost that output since; only
		// one whose start it saw and that never printed has nothing to show.
		c.mu.Lock()
		start, started := c.backgroundStarted[task.RunID]
		c.mu.Unlock()
		return ports.ChatBackgroundTaskOutput{Truncated: !started || start.printed}, nil
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

// rememberBackgroundStartLocked records that this controller saw an exec
// item start. Caller holds mu.
func (c *conversation) rememberBackgroundStartLocked(runID string) {
	if _, started := c.backgroundStarted[runID]; runID == "" || started {
		return
	}
	if c.backgroundStarted == nil {
		c.backgroundStarted = make(map[string]backgroundStart)
	}
	c.backgroundStarted[runID] = backgroundStart{}
	c.backgroundStartOrder = append(c.backgroundStartOrder, runID)
	for len(c.backgroundStartOrder) > backgroundStartsKept {
		delete(c.backgroundStarted, c.backgroundStartOrder[0])
		c.backgroundStartOrder = c.backgroundStartOrder[1:]
	}
}

// markBackgroundPrintedLocked records that a started exec item printed.
// Caller holds mu.
func (c *conversation) markBackgroundPrintedLocked(runID string) {
	if _, started := c.backgroundStarted[runID]; started {
		c.backgroundStarted[runID] = backgroundStart{printed: true}
	}
}

func (c *conversation) observeBackgroundNotification(n notification) {
	if n.Method == codexproto.MethodItemCommandExecutionOutputDelta {
		var delta codexproto.CommandExecutionOutputDeltaNotification
		if json.Unmarshal(n.Params, &delta) == nil && (delta.ThreadID == "" || delta.ThreadID == c.threadID) {
			c.mu.Lock()
			_, started := c.backgroundStarted[delta.ItemID]
			c.markBackgroundPrintedLocked(delta.ItemID)
			c.mu.Unlock()
			if !started {
				// Attached after the command started, so its first output is gone.
				c.backgroundOutput.MarkDropped(delta.ItemID)
			}
			c.backgroundOutput.Append(delta.ItemID, delta.Delta)
		}
		return
	}
	if n.Method != "item/started" && n.Method != "item/completed" && n.Method != "turn/completed" {
		return
	}
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Turn     struct {
			ID string `json:"id"`
		} `json:"turn"`
		Item struct {
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
	if n.Method == "turn/completed" {
		if params.Turn.ID == "" {
			return
		}
		c.mu.Lock()
		for id, command := range c.backgroundCommands {
			if command.TurnID == params.Turn.ID && command.CallCompletedAt.IsZero() {
				command.CallCompletedAt = now
				c.backgroundCommands[id] = command
			}
		}
		c.mu.Unlock()
		return
	}
	if params.Item.Type == "commandExecution" {
		c.mu.Lock()
		if n.Method == "item/started" {
			observed := c.backgroundCommands[params.Item.ID]
			if observed.StartedAt == nil {
				observed.StartedAt = &now
			}
			if observed.Description == "" {
				observed.Description = domain.SanitizeTaskDescription(params.Item.Command)
			}
			if observed.TurnID == "" {
				observed.TurnID = params.TurnID
			}
			c.rememberBackgroundCommand(params.Item.ID, observed)
			c.rememberBackgroundStartLocked(params.Item.ID)
			c.mu.Unlock()
			return
		}
		observed := c.backgroundCommands[params.Item.ID]
		if params.Item.ExitCode == nil {
			// The launching tool can complete while unified-exec keeps its process.
			// Only a later inventory can confirm that it actually outlived this call.
			if observed.CallCompletedAt.IsZero() {
				observed.CallCompletedAt = now
			}
			if observed.TurnID == "" {
				observed.TurnID = params.TurnID
			}
			c.rememberBackgroundCommand(params.Item.ID, observed)
			c.mu.Unlock()
			return
		}
		delete(c.backgroundCommands, params.Item.ID)
		c.mu.Unlock()
		// Foreground completions can carry process IDs too. Inventory membership,
		// rather than a handle alone, is the evidence of background execution.
		if params.Item.ProcessID == "" {
			return
		}
		c.rememberBackgroundRun(params.Item.ProcessID, params.Item.ID)
		if params.Item.AggregatedOutput != nil && *params.Item.AggregatedOutput != "" {
			// The whole output fills what the stream missed, such as lines after
			// the delta cap. Codex can still drop the first chunk from both.
			c.backgroundOutput.Replace(params.Item.ID, *params.Item.AggregatedOutput)
			c.mu.Lock()
			c.markBackgroundPrintedLocked(params.Item.ID)
			c.mu.Unlock()
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
		if observed.Listed {
			// Prefer the native inventory label over the completion's shell wrapper.
			c.backgroundObserved.Update(ports.ChatBackgroundTask{ID: params.Item.ProcessID, RunID: params.Item.ID, Kind: "shell", Description: observed.Description, State: state, StartedAt: observed.StartedAt}, now)
		} else {
			c.backgroundObserved.Finish(params.Item.ProcessID, state, now)
		}
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

// rememberBackgroundLaunchHistory seeds launch metadata, never tasks or transcript events.
func (c *conversation) rememberBackgroundLaunchHistory(turns []codexproto.Turn) {
	// Reconnects may have missed command events. A settled turn is evidence
	// that its launching calls ended; a later list must still confirm each job.
	// Do not invent a start time or introduce a task from history alone.
	c.mu.Lock()
	boundary := time.Now()
	for _, turn := range turns {
		// The display normalizer maps unknown statuses to failed; that fallback
		// is not evidence that a launching call has actually finished.
		settled := false
		switch turn.Status {
		case codexproto.TurnStatusCompleted, codexproto.TurnStatusFailed, codexproto.TurnStatusInterrupted:
			settled = true
		case codexproto.TurnStatusInProgress:
			// Retain identity only: a later turn completion can qualify a job,
			// but this snapshot supplies no background evidence for a live call.
		default:
			continue
		}
		for _, item := range turn.Items {
			if item.Type != codexproto.ThreadItemTypeCommandExecution || item.ExitCode != nil {
				continue
			}
			id := deref(item.ID)
			command := c.backgroundCommands[id]
			if settled && command.CallCompletedAt.IsZero() {
				command.CallCompletedAt = boundary
			}
			if command.TurnID == "" {
				command.TurnID = turn.ID
			}
			c.rememberBackgroundCommand(id, command)
		}
	}
	c.mu.Unlock()
}

// readBackgroundLaunchHistory never replays messages or changes provider state.
func (c *conversation) readBackgroundLaunchHistory(ctx context.Context) (codexproto.Thread, error) {
	var response codexproto.ThreadReadResponse
	includeTurns := true
	if err := c.conn.request(ctx, codexproto.MethodThreadRead, codexproto.ThreadReadParams{ThreadID: c.threadID, IncludeTurns: &includeTurns}, &response); err != nil {
		return response.Thread, fmt.Errorf("background launch history: %w", err)
	}
	if response.Thread.ID != c.threadID {
		return response.Thread, fmt.Errorf("background history returned %q, requested %q", response.Thread.ID, c.threadID)
	}
	if response.Thread.Turns == nil {
		return response.Thread, errors.New("background history is missing turns")
	}
	return response.Thread, nil
}

// A reconnect can enumerate surviving terminals without matching replayed item
// IDs or exit codes. Require two idle snapshots with the same turn identities,
// bracketing the inventory, so a new foreground turn cannot qualify its shells.
func idleBackgroundTurns(thread codexproto.Thread) ([]string, bool) {
	if thread.Status.Type != "" && thread.Status.Type != codexproto.ThreadStatusTypeIdle {
		return nil, false
	}
	if thread.Status.Type == "" && len(thread.Turns) == 0 {
		return nil, false
	}
	ids := make([]string, 0, len(thread.Turns))
	for _, turn := range thread.Turns {
		if turn.ID == "" {
			return nil, false
		}
		switch turn.Status {
		case codexproto.TurnStatusCompleted, codexproto.TurnStatusFailed, codexproto.TurnStatusInterrupted:
		default:
			return nil, false
		}
		ids = append(ids, turn.ID)
	}
	return ids, true
}
