package acp

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Subagents that an agent keeps only in its own records, in the shape AO's
// chat already reads for Claude's.
//
// Some agents (DeepSeek Harness) launch a subagent with an ordinary tool call
// that completes as soon as the child starts, and send nothing of the child's
// work over ACP: not its steps, not its report, not how it ended. The agent
// records all of it in its own session store as the child works. The binding
// knows how to read that store (SubagentRecords); this file reads it while a
// turn runs and files what each child did under its launch:
//
//   - its tool calls become steps with detail.parentProviderItemId;
//   - what it says becomes nested "Subagent response" steps;
//   - each run's end becomes a SubagentHandback step, completed with the
//     child's report, failed with its reason, or cancelled.
//
// Steps are filed on the parent's running turn. That is right only for an agent
// whose prompt returns once every subagent it started is idle, as DeepSeek's
// does, so the turn outlives the children it holds and settles their rows.
// Before the turn completes, the records are read one last time.

// SubagentRecords is a binding's knowledge of subagents an agent records only
// in its own store. Launch and Open are required.
type SubagentRecords struct {
	// Launch reports whether a tool call starts a subagent, and how it shows.
	Launch func(RecordedCall) (RecordedLaunch, bool)
	// Resumes reports whether a tool call can give an earlier subagent more
	// work, so a turn that makes one follows the records even when it
	// launches nothing. Optional.
	Resumes func(RecordedCall) bool
	// Open returns a reader of the subagents session starts. env and workspace
	// are the conversation's launch environment and working directory.
	Open func(env map[string]string, workspace, session string) SubagentRecordReader
}

// RecordedCall is what SubagentRecords reads of a tool call.
type RecordedCall struct {
	Title    string
	Kind     acpsdk.ToolKind
	RawInput any
}

// RecordedLaunch is how a launch shows: the tool name the renderer lists as a
// launch, a short title, the task, and the agent type (empty for the default).
type RecordedLaunch struct {
	ToolName, Title, Prompt, AgentType string
}

// SubagentRecordReader reads what an agent recorded of its subagents. It is
// called from one goroutine at a time.
type SubagentRecordReader interface {
	// Read returns what the subagents did since the last Read, in order.
	// launched holds the launches seen on this connection, by tool call id:
	// their children are followed from their start. Any other child is
	// followed from the first run still open, or starting, after the first
	// Read, so work an earlier connection already filed is not filed again.
	Read(ctx context.Context, launched map[string]bool) ([]RecordedUpdate, error)
	// Busy reports whether a followed child has a run that has not ended yet.
	Busy() bool
}

// RecordedUpdate is one thing a child did, filed under the launch that
// started it. Exactly one of Update and End is set.
type RecordedUpdate struct {
	// Launch is the launch's tool call id on the conversation's session.
	Launch string
	// Child names the child, scoping its tool call and message ids.
	Child string
	// Update is a tool_call, tool_call_update, or agent_message_chunk carrying
	// a messageId. Anything else is ignored.
	Update *acpsdk.SessionUpdate
	// End closes one run of the child.
	End *RecordedEnd
}

// RecordedEnd is how one run of a child ended. ID names the run stably across
// reads. Report is the child's closing message, or why it failed.
type RecordedEnd struct {
	ID     string
	Status RecordedEndStatus
	Report string
}

// RecordedEndStatus is how a child's run ended.
type RecordedEndStatus string

const (
	RecordedCompleted RecordedEndStatus = "completed"
	RecordedFailed    RecordedEndStatus = "failed"
	RecordedCancelled RecordedEndStatus = "cancelled"
)

const (
	subagentRecordPollEvery = 300 * time.Millisecond
	// subagentRecordDrainFor bounds the last read before a turn completes:
	// the agent writes a child's records in batches, so its last ones can land
	// just after the prompt returns.
	subagentRecordDrainFor   = 3 * time.Second
	subagentRecordDrainEvery = 100 * time.Millisecond
	subagentHandbackTool     = "SubagentHandback"
	subagentResponseSummary  = "Subagent response"
)

// recordFollower is what the conversation knows of its recorded subagents. It
// is created on the first launch, or on reattaching to a running turn, and
// guarded by conversation.mu except where noted.
type recordFollower struct {
	reader   SubagentRecordReader
	launched map[string]bool
	// stop and done belong to the running poll, nil when none runs.
	stop chan struct{}
	done chan struct{}
	// readMu serializes reads, so the poll and the last read never interleave.
	// Held without mu. tools and said are guarded by it.
	readMu chan struct{}
	tools  map[string]map[string]*toolState
	said   map[string]string
}

func recordedCallView(tool *toolState) RecordedCall {
	return RecordedCall{Title: tool.title, Kind: tool.kind, RawInput: tool.rawInput}
}

// decorateRecordedLaunch marks a launch in its activity detail: the renderer's
// launch name, the child's task as input, and background, because the launch
// completes when the child starts, not when it finishes.
func (c *conversation) decorateRecordedLaunch(tool *toolState, detail map[string]any) {
	if c.subagentRecords == nil {
		return
	}
	launch, ok := c.subagentRecords.Launch(recordedCallView(tool))
	if !ok {
		return
	}
	detail["providerToolName"] = launch.ToolName
	detail["background"] = true
	input := map[string]any{"prompt": launch.Prompt}
	if launch.Title != "" {
		input["description"] = launch.Title
	}
	detail["input"] = cappedPayload(input)
	if launch.AgentType != "" {
		detail["subagentType"] = launch.AgentType
	}
}

// observeRecordedLaunch starts following the records once a turn launches a
// subagent, or gives an earlier one more work.
func (c *conversation) observeRecordedLaunch(tool *toolState) {
	if c.subagentRecords == nil {
		return
	}
	view := recordedCallView(tool)
	_, launches := c.subagentRecords.Launch(view)
	if !launches && (c.subagentRecords.Resumes == nil || !c.subagentRecords.Resumes(view)) {
		return
	}
	c.mu.Lock()
	follower := c.recordFollowerLocked()
	if launches {
		follower.launched[tool.id] = true
	}
	c.mu.Unlock()
	c.pollSubagentRecords()
}

// recordFollowerLocked returns the follower, creating it. The caller holds mu.
func (c *conversation) recordFollowerLocked() *recordFollower {
	if c.recordFollower == nil {
		c.recordFollower = &recordFollower{
			reader:   c.subagentRecords.Open(c.subagentRecordEnv, c.subagentRecordCwd, c.sessionID),
			launched: map[string]bool{},
			readMu:   make(chan struct{}, 1),
			tools:    map[string]map[string]*toolState{},
			said:     map[string]string{},
		}
	}
	return c.recordFollower
}

// followRecordsOnReattachLocked follows the records of a turn an earlier connection
// started: its children may still be working. The caller holds mu.
func (c *conversation) followRecordsOnReattachLocked() {
	if c.subagentRecords != nil {
		c.recordFollowerLocked()
	}
}

// pollSubagentRecords reads the records while a turn runs, once the
// conversation has subagents to follow. It is a no-op without a running turn,
// a follower, or with a poll already running.
func (c *conversation) pollSubagentRecords() {
	c.mu.Lock()
	follower := c.recordFollower
	if follower == nil || follower.stop != nil || c.activeTurn == "" {
		c.mu.Unlock()
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	follower.stop, follower.done = stop, done
	c.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-stop:
		case <-c.closing:
		}
		cancel()
	}()
	go func() {
		defer close(done)
		ticker := time.NewTicker(subagentRecordPollEvery)
		defer ticker.Stop()
		// The first read is at once: it is the baseline for children an earlier
		// connection filed, and a child can start, and finish, within a tick.
		for {
			c.readSubagentRecords(ctx, follower)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// drainSubagentRecords stops the poll and reads the records until no followed
// child is mid-run, or for subagentRecordDrainFor, so the turn completes with
// its children's last steps and reports filed on it.
func (c *conversation) drainSubagentRecords() {
	c.mu.Lock()
	follower := c.recordFollower
	if follower == nil {
		c.mu.Unlock()
		return
	}
	stop, done := follower.stop, follower.done
	follower.stop, follower.done = nil, nil
	c.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	ctx, cancel := context.WithTimeout(context.Background(), subagentRecordDrainFor)
	defer cancel()
	for {
		c.readSubagentRecords(ctx, follower)
		if !follower.reader.Busy() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-c.closing:
			return
		case <-time.After(subagentRecordDrainEvery):
		}
	}
}

func (c *conversation) readSubagentRecords(ctx context.Context, follower *recordFollower) {
	select {
	case follower.readMu <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-follower.readMu }()
	c.mu.Lock()
	launched := make(map[string]bool, len(follower.launched))
	for id := range follower.launched {
		launched[id] = true
	}
	c.mu.Unlock()
	updates, err := follower.reader.Read(ctx, launched)
	if err != nil && ctx.Err() == nil && c.log != nil {
		c.log.Debug("ACP subagent records unreadable", "error", err)
	}
	for _, update := range updates {
		c.mu.Lock()
		turnID := c.activeTurn
		c.mu.Unlock()
		if turnID == "" {
			// The turn has ended; its children's work is filed with it or not at all.
			return
		}
		c.emitRecordedUpdate(turnID, follower, update)
	}
}

// emitRecordedUpdate files one recorded update under its launch. The caller
// holds follower.readMu.
func (c *conversation) emitRecordedUpdate(turnID string, follower *recordFollower, recorded RecordedUpdate) {
	if recorded.Launch == "" || recorded.Child == "" {
		return
	}
	launch := c.providerItemID(recorded.Launch)
	if end := recorded.End; end != nil {
		c.emit(c.recordedHandback(turnID, launch, recorded.Child, *end))
		return
	}
	update := recorded.Update
	if update == nil {
		return
	}
	switch {
	case update.ToolCall != nil, update.ToolCallUpdate != nil:
		tools := follower.tools[recorded.Child]
		if tools == nil {
			tools = map[string]*toolState{}
			follower.tools[recorded.Child] = tools
		}
		var stored *toolState
		if update.ToolCall != nil {
			stored = newToolState(update.ToolCall)
			tools[stored.id] = stored
		} else {
			stored = mergeToolState(tools, update.ToolCallUpdate)
		}
		snapshot := *stored
		tool := &snapshot
		if toolTerminal(tool.status) {
			delete(tools, tool.id)
		}
		event := c.toolEvent(turnID, tool, toolTerminal(tool.status))
		event.ProviderItemID = c.providerItemID("subagent-tool:" + lengthPrefixedTuple(recorded.Child, tool.id))
		c.emit(withParentProviderItem(event, launch))

	case update.AgentMessageChunk != nil:
		chunk := update.AgentMessageChunk
		if chunk.MessageId == nil || *chunk.MessageId == "" {
			return
		}
		delta := contentText(chunk.Content)
		if delta == "" {
			return
		}
		key := lengthPrefixedTuple(recorded.Child, *chunk.MessageId)
		follower.said[key] += delta
		text := strings.TrimSpace(follower.said[key])
		detail, _ := json.Marshal(map[string]any{"parentProviderItemId": launch, "nestedAgent": true})
		c.emit(ports.ChatEvent{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turnID,
			ProviderItemID: c.providerItemID("subagent-message:" + key),
			ActivityKind:   domain.ActivityKindMCPTool, ActivityStatus: domain.ActivityStatusCompleted,
			Summary: subagentResponseSummary, Detail: detail, Text: text,
		})
	}
}

// recordedHandback is a child's report on one run, to the launch that started
// it. Each run reports under its own item, so a later run adds a report rather
// than rewriting an earlier one.
func (c *conversation) recordedHandback(turnID, launch, child string, end RecordedEnd) ports.ChatEvent {
	detail := map[string]any{
		"parentProviderItemId": launch,
		"providerToolName":     subagentHandbackTool,
	}
	if message := strings.TrimSpace(end.Report); message != "" {
		detail["input"] = map[string]any{"message": message}
	}
	status := domain.ActivityStatusCompleted
	switch end.Status {
	case RecordedFailed:
		status = domain.ActivityStatusFailed
	case RecordedCancelled:
		status = domain.ActivityStatusCancelled
	}
	encoded, _ := json.Marshal(detail)
	return ports.ChatEvent{
		Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turnID,
		ProviderItemID: c.providerItemID("subagent-handback:" + lengthPrefixedTuple(child, end.ID)),
		ActivityKind:   domain.ActivityKindMCPTool, ActivityStatus: status,
		Summary: subagentHandbackTool, Detail: encoded,
	}
}

// withParentProviderItem files a step under the launch whose item id is parent.
func withParentProviderItem(event ports.ChatEvent, parent string) ports.ChatEvent {
	var detail map[string]any
	if len(event.Detail) > 0 && json.Unmarshal(event.Detail, &detail) != nil {
		return event
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["parentProviderItemId"] = parent
	if encoded, err := json.Marshal(detail); err == nil {
		event.Detail = encoded
	}
	return event
}
