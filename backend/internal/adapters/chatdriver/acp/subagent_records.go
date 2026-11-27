package acp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
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
// Before the turn completes, the records are read one last time. A record that
// cannot be delivered in its turn is held for that turn, never filed on a later
// one.

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

// RecordedCall is what a binding reads of a tool call to tell a launch.
type RecordedCall struct {
	Title     string
	Kind      acpsdk.ToolKind
	RawInput  any
	RawOutput any
}

// RecordedLaunch is how a launch shows: the tool name the renderer lists as a
// launch, a short title, the task, and the agent type (empty for the default).
// Foreground is set for a launch that completes only when its child does, so
// its own status says the child finished; any other completes when the child
// starts.
type RecordedLaunch struct {
	ToolName, Title, Prompt, AgentType string
	Foreground                         bool
}

// SubagentRecordReader reads what an agent recorded of its subagents. It is
// called from one goroutine at a time.
type SubagentRecordReader interface {
	// Read returns what the subagents did since the last Read, in order: the
	// work of the conversation's current turn. Work recorded for an earlier
	// turn is never returned, whether or not a connection filed it then, so a
	// reader opened mid-turn (a reattach) files the whole turn, and only it.
	Read(ctx context.Context) ([]RecordedUpdate, error)
	// Busy reports whether a child of the current turn has a run, or a run's
	// end, that Read has not returned yet.
	Busy() bool
}

// RecordedUpdate is one thing a child did, filed under the launch that
// started it. Exactly one of Update and End is set.
type RecordedUpdate struct {
	// Launch is the launch's tool call id on the conversation's session.
	Launch string
	// Child names the child, scoping its tool call and message ids.
	Child string
	// Run names the child's run the update or end belongs to: an End
	// releases what was held for its own run only.
	Run string
	// Update is a tool_call, tool_call_update, or agent_message_chunk carrying
	// a messageId. Anything else is ignored.
	Update *acpsdk.SessionUpdate
	// End closes one run of the child and releases what was held for that
	// run's messages and calls.
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

// The ways a child's run can end.
const (
	RecordedCompleted RecordedEndStatus = "completed"
	RecordedFailed    RecordedEndStatus = "failed"
	RecordedCancelled RecordedEndStatus = "cancelled"
)

const (
	subagentRecordPollEvery  = 300 * time.Millisecond
	subagentRecordDrainEvery = 100 * time.Millisecond
	subagentHandbackTool     = "SubagentHandback"
	subagentResponseSummary  = "Subagent response"
)

// subagentRecordDrainFor bounds the last read before a turn completes, joining
// the poll and delivering included: the agent writes a child's records in
// batches, so its last ones can land just after the prompt returns. A var so
// tests can shorten it.
var subagentRecordDrainFor = 3 * time.Second

// recordFollower is what the conversation knows of its recorded subagents. It
// is created on the first launch, or on reattaching to a running turn, and
// guarded by conversation.mu except where noted.
type recordFollower struct {
	reader SubagentRecordReader
	// stop, cancel and done belong to the running poll, nil when none runs.
	stop   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
	// emitMu is held across each record's last checks and its send. The last
	// read of a turn marks it closed under emitMu, so once it has, no record
	// of that turn can still go out, however late its read comes back.
	emitMu     sync.Mutex
	closedTurn string
	// readMu serializes reads and Busy, so the poll and the last read never
	// interleave. Held without mu. The fields below are guarded by it.
	readMu chan struct{}
	// tools and said are each run's open tool calls and messages so far, by
	// child and run.
	tools map[string]map[string]*toolState
	said  map[string]map[string]string
	// backlog holds what was read but not delivered, and the turn it belongs to.
	backlog     []RecordedUpdate
	backlogTurn string
}

// closeTurn ends delivery for turnID: it waits for a send in flight, and no
// record of that turn is sent after it returns.
func (f *recordFollower) closeTurn(turnID string) {
	f.emitMu.Lock()
	f.closedTurn = turnID
	f.emitMu.Unlock()
}

func recordedCallView(tool *toolState) RecordedCall {
	return RecordedCall{Title: tool.title, Kind: tool.kind, RawInput: tool.rawInput, RawOutput: tool.rawOutput}
}

// subagentLaunch tells a launch with whichever subagent support the binding
// has: one recorded in the agent's store, or one announced over ACP.
func (c *conversation) subagentLaunch(call RecordedCall) (RecordedLaunch, bool) {
	switch {
	case c.subagentRecords != nil:
		return c.subagentRecords.Launch(call)
	case c.spawnedSubagents != nil:
		return c.spawnedSubagents.Launch(call)
	}
	return RecordedLaunch{}, false
}

// decorateRecordedLaunch marks a launch in its activity detail: the renderer's
// launch name, the child's task as input, and background, because the launch
// completes when the child starts, not when it finishes.
func (c *conversation) decorateRecordedLaunch(tool *toolState, detail map[string]any) {
	launch, ok := c.subagentLaunch(recordedCallView(tool))
	if !ok {
		return
	}
	detail["providerToolName"] = launch.ToolName
	if !launch.Foreground {
		detail["background"] = true
	}
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
		// Announced subagents need no reading: their frames file themselves.
		return
	}
	view := recordedCallView(tool)
	_, launches := c.subagentRecords.Launch(view)
	if !launches && (c.subagentRecords.Resumes == nil || !c.subagentRecords.Resumes(view)) {
		return
	}
	c.mu.Lock()
	c.ensureRecordFollowerLocked()
	c.mu.Unlock()
	c.pollSubagentRecords()
}

// ensureRecordFollowerLocked creates the follower once. An agent that
// announces its subagents has no store to read: its follower has no reader.
// The caller holds mu.
func (c *conversation) ensureRecordFollowerLocked() {
	if c.recordFollower == nil {
		var reader SubagentRecordReader
		if c.subagentRecords != nil {
			reader = c.subagentRecords.Open(c.subagentRecordEnv, c.subagentRecordCwd, c.sessionID)
		}
		c.recordFollower = &recordFollower{
			reader: reader,
			readMu: make(chan struct{}, 1),
			tools:  map[string]map[string]*toolState{},
			said:   map[string]map[string]string{},
		}
	}
}

// followRecordsOnReattachLocked follows the records of a turn an earlier
// connection started: its children may still be working, or have finished
// while no connection read them. The caller holds mu.
func (c *conversation) followRecordsOnReattachLocked() {
	if c.subagentRecords != nil {
		c.ensureRecordFollowerLocked()
	}
}

// pollSubagentRecords reads the records while a turn runs, once the
// conversation has subagents to follow. It is a no-op without a running turn
// or a follower, with a poll already running, or once the turn is settling:
// the last read takes over then.
func (c *conversation) pollSubagentRecords() {
	c.mu.Lock()
	follower := c.recordFollower
	turnID := c.activeTurn
	if follower == nil || follower.reader == nil || follower.stop != nil || turnID == "" || c.settlingTurn == turnID {
		c.mu.Unlock()
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	follower.stop, follower.cancel, follower.done = stop, cancel, done
	c.mu.Unlock()
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
		// The first read is at once: a child can start, and finish, within a tick.
		for {
			c.readSubagentRecords(ctx, follower, turnID)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// drainSubagentRecords stops the poll and reads the records until no child of
// the turn is mid-run, so the turn completes with its children's last steps
// and reports filed on it. subagentRecordDrainFor bounds all of it.
func (c *conversation) drainSubagentRecords() {
	c.mu.Lock()
	follower := c.recordFollower
	if follower == nil || follower.reader == nil {
		c.mu.Unlock()
		return
	}
	turnID := c.activeTurn
	stop, pollCancel, done := follower.stop, follower.cancel, follower.done
	follower.stop, follower.cancel, follower.done = nil, nil, nil
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), subagentRecordDrainFor)
	// However the last read ends, every reader of this turn is cancelled and
	// any send in flight finishes before the turn goes on to settle.
	defer func() {
		cancel()
		if pollCancel != nil {
			pollCancel()
		}
		follower.closeTurn(turnID)
	}()
	if stop != nil {
		// Stopping cancels the poll's read and delivery, so it returns promptly.
		close(stop)
		select {
		case <-done:
		case <-ctx.Done():
		}
	}
	for {
		// The read runs apart, so a store that stalls (a hung mount) cannot hold the
		// turn past the deadline. One left behind is cancelled: what it reads is
		// held for this turn, never delivered on a later one.
		type result struct{ busy, ok bool }
		read := make(chan result, 1)
		go func() {
			busy, ok := c.readSubagentRecords(ctx, follower, turnID)
			read <- result{busy, ok}
		}()
		var got result
		select {
		case got = <-read:
		case <-ctx.Done():
			return
		}
		if !got.ok || !got.busy {
			break
		}
		select {
		case <-ctx.Done():
		case <-c.closing:
		case <-time.After(subagentRecordDrainEvery):
			continue
		}
		break
	}
	c.dropRecordBacklog(follower)
}

// readSubagentRecords reads and delivers what is new for turnID, the turn the
// read was started for, and reports whether a child is still mid-run. ok is
// false when the read could not run for that turn: ctx ended, or another turn
// is running by the time it got readMu.
func (c *conversation) readSubagentRecords(ctx context.Context, follower *recordFollower, turnID string) (busy, ok bool) {
	select {
	case follower.readMu <- struct{}{}:
	case <-ctx.Done():
		return false, false
	}
	defer func() { <-follower.readMu }()
	// Both cases above can be ready at once: a cancelled read that won readMu
	// must not read for whatever turn runs now.
	if ctx.Err() != nil {
		return false, false
	}
	c.mu.Lock()
	active := c.activeTurn
	c.mu.Unlock()
	if turnID == "" || active != turnID {
		return false, false
	}
	if len(follower.backlog) > 0 && follower.backlogTurn != turnID {
		// Held for a turn that has ended: it is that turn's, not this one's.
		c.logDroppedRecords(len(follower.backlog), follower.backlogTurn)
		follower.backlog, follower.backlogTurn = nil, ""
	}
	updates, err := follower.reader.Read(ctx)
	if err != nil && ctx.Err() == nil && c.log != nil {
		c.log.Debug("ACP subagent records unreadable", "error", err)
	}
	pending := slices.Concat(follower.backlog, updates)
	follower.backlog, follower.backlogTurn = nil, ""
	for i, update := range pending {
		if !c.emitRecordedUpdate(ctx, turnID, follower, update) {
			follower.backlog, follower.backlogTurn = append([]RecordedUpdate(nil), pending[i:]...), turnID
			break
		}
	}
	return follower.reader.Busy() || len(follower.backlog) > 0, true
}

// dropRecordBacklog discards what the turn could not deliver before it
// settled, saying so: it cannot be filed on the turn it belongs to.
func (c *conversation) dropRecordBacklog(follower *recordFollower) {
	select {
	case follower.readMu <- struct{}{}:
	default:
		// A poll that outlived the deadline still holds it; its backlog is
		// tagged with this turn and dropped by the next read.
		return
	}
	defer func() { <-follower.readMu }()
	if len(follower.backlog) > 0 {
		c.logDroppedRecords(len(follower.backlog), follower.backlogTurn)
		follower.backlog, follower.backlogTurn = nil, ""
	}
}

func (c *conversation) logDroppedRecords(count int, turnID string) {
	if c.log != nil {
		c.log.Warn("dropped ACP subagent records: their turn settled first", "count", count, "turn", turnID)
	}
}

// emitRecordedUpdate files one recorded update under its launch, reporting
// whether it was delivered. A child's state changes only once it is, so an
// update held back is delivered whole on the next try. The caller holds
// follower.readMu.
func (c *conversation) emitRecordedUpdate(ctx context.Context, turnID string, follower *recordFollower, recorded RecordedUpdate) bool {
	if recorded.Launch == "" || recorded.Child == "" {
		return true
	}
	launch := c.providerItemID(recorded.Launch)
	run := lengthPrefixedTuple(recorded.Child, recorded.Run)
	if end := recorded.End; end != nil {
		if !c.emitRecord(ctx, follower, c.recordedHandback(turnID, launch, recorded.Child, *end)) {
			return false
		}
		// The run is over: what was held for its messages and calls is not
		// needed again. Another run's is kept.
		delete(follower.said, run)
		delete(follower.tools, run)
		return true
	}
	update := recorded.Update
	if update == nil {
		return true
	}
	switch {
	case update.ToolCall != nil, update.ToolCallUpdate != nil:
		// Merge into a copy; the child's state takes it once it is delivered.
		scratch := map[string]*toolState{}
		var tool *toolState
		if update.ToolCall != nil {
			tool = newToolState(update.ToolCall)
		} else {
			id := string(update.ToolCallUpdate.ToolCallId)
			if stored := follower.tools[run][id]; stored != nil {
				clone := *stored
				scratch[id] = &clone
			}
			tool = mergeToolState(scratch, update.ToolCallUpdate)
		}
		event := c.toolEvent(turnID, tool, toolTerminal(tool.status))
		event.ProviderItemID = c.providerItemID("subagent-tool:" + lengthPrefixedTuple(recorded.Child, tool.id))
		if !c.emitRecord(ctx, follower, withParentProviderItem(event, launch)) {
			return false
		}
		tools := follower.tools[run]
		if toolTerminal(tool.status) {
			delete(tools, tool.id)
			return true
		}
		if tools == nil {
			tools = map[string]*toolState{}
			follower.tools[run] = tools
		}
		tools[tool.id] = tool

	case update.AgentMessageChunk != nil:
		chunk := update.AgentMessageChunk
		if chunk.MessageId == nil || *chunk.MessageId == "" {
			return true
		}
		delta := contentText(chunk.Content)
		if delta == "" {
			return true
		}
		said := follower.said[run][*chunk.MessageId] + delta
		key := lengthPrefixedTuple(recorded.Child, *chunk.MessageId)
		detail, _ := json.Marshal(map[string]any{"parentProviderItemId": launch, "nestedAgent": true})
		if !c.emitRecord(ctx, follower, ports.ChatEvent{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turnID,
			ProviderItemID: c.providerItemID("subagent-message:" + key),
			ActivityKind:   domain.ActivityKindMCPTool, ActivityStatus: domain.ActivityStatusCompleted,
			Summary: subagentResponseSummary, Detail: detail, Text: strings.TrimSpace(said),
		}) {
			return false
		}
		messages := follower.said[run]
		if messages == nil {
			messages = map[string]string{}
			follower.said[run] = messages
		}
		messages[*chunk.MessageId] = said
	}
	return true
}

// emitRecord delivers one recorded event under the follower's emitMu, unless
// its turn has closed: see recordFollower.emitMu.
func (c *conversation) emitRecord(ctx context.Context, follower *recordFollower, event ports.ChatEvent) bool {
	follower.emitMu.Lock()
	defer follower.emitMu.Unlock()
	if event.ProviderTurnID != "" && event.ProviderTurnID == follower.closedTurn {
		return false
	}
	return c.emitWithin(ctx, event)
}

// emitWithin is emit for work that must not outlast ctx: it waits for the
// event lock and the consumer only until ctx ends, and reports whether the
// event was queued.
func (c *conversation) emitWithin(ctx context.Context, event ports.ChatEvent) bool {
	// A history capture is checked under its lock, and nothing is captured once
	// ctx has ended.
	if !lockWithin(ctx, c.historyMu.TryLock, c.historyMu.Unlock) {
		return false
	}
	captured := false
	if ctx.Err() == nil {
		captured = c.captureHistoryEventLocked(event)
	}
	c.historyMu.Unlock()
	if captured {
		return true
	}
	// A writer can hold the event lock while it waits on the consumer itself.
	if !lockWithin(ctx, c.eventMu.TryRLock, c.eventMu.RUnlock) {
		return false
	}
	defer c.eventMu.RUnlock()
	// With ctx over, a free slot must not win the select below.
	if c.eventsClosed || ctx.Err() != nil {
		return false
	}
	var connDone <-chan struct{}
	if c.conn != nil {
		connDone = c.conn.Done()
	}
	select {
	case c.events <- event:
		return true
	case <-ctx.Done():
	case <-connDone:
	case <-c.closing:
	}
	return false
}

// lockWithin takes a lock with try until ctx ends, reporting whether it holds
// it: a lock some other path holds for long cannot keep a record past its
// deadline.
func lockWithin(ctx context.Context, try func() bool, unlock func()) bool {
	for !try() {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		unlock()
		return false
	}
	return true
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
