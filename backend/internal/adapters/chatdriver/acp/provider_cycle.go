package acp

import (
	"encoding/json"
	"maps"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A provider cycle is work the agent starts on its own while AO has no turn in
// flight: Antigravity and Claude Code both answer a background command or task
// that finished after the turn ended. ACP has no prompt, and so no turn, for
// it. Its output stays turn-less, as before, but it gets its own item ids, so a
// later cycle does not append to an earlier cycle's message, and its message
// and reasoning are settled when it ends instead of streaming forever.
//
// Antigravity marks neither end of a cycle. It closes every model response with
// a usage_update, and a response that calls a tool reports the call right
// after it, so a usage_update with none of the cycle's tools open, followed by
// providerCycleSettleGrace of quiet, ends the cycle. providerCycleQuietWait is
// the fallback for an agent that sends no usage_update. Either way, the next
// AO turn ends an open cycle before it starts. An agent that reports its own
// running and idle states (Claude Code) ends the cycle with its idle instead.
var (
	providerCycleSettleGrace = 3 * time.Second
	providerCycleQuietWait   = 30 * time.Second
)

// providerCyclePrefix starts every cycle id, so a compaction the agent ran in a
// cycle, tracked by the cycle's id, keeps its row turn-less like the cycle's
// other items.
const providerCyclePrefix = "cycle-"

// compactionRowTurn is the turn a compaction's row belongs to: the turn it ran
// in, or none for one in a provider cycle.
func compactionRowTurn(key string) string {
	if strings.HasPrefix(key, providerCyclePrefix) {
		return ""
	}
	return key
}

type providerCycle struct {
	id       string
	items    map[string]bool // message, reasoning and subagent-message ids
	tools    map[string]bool // tool calls started in this cycle
	timerGen int
	timer    *time.Timer
	lastSeen time.Time
	// settled is what the cycle left open when it was detached, with any text
	// that arrived after; nil while it is open. Guarded by mu.
	settled *settledCycle
}

// settledCycle is the text of a cycle's items, to complete them with.
type settledCycle struct {
	messages map[string]string
	thoughts map[string]string
	nested   map[string]nestedMessageState
}

// enterProviderCycleLocked returns the cycle a turn-less update belongs to,
// opening one for agent output. Updates that are not output (usage, a late
// update for an earlier turn's tool) join an open cycle but never open one. It
// stops the cycle's settle timer: the update re-arms it once it has been
// applied. The caller holds mu, and read activeTurn as empty under that same
// hold, so a cycle never opens once a turn has started.
func (c *conversation) enterProviderCycleLocked(update acpsdk.SessionUpdate) (*providerCycle, bool) {
	output := update.AgentMessageChunk != nil || update.AgentThoughtChunk != nil ||
		update.ToolCall != nil || update.Plan != nil
	if update.ToolCallUpdate != nil && c.tools[string(update.ToolCallUpdate.ToolCallId)] == nil {
		// A tool AO never saw start is output of the cycle it belongs to.
		output = true
	}
	if update.SessionInfoUpdate != nil && compactionStatusMeta(update.SessionInfoUpdate.Meta) != nil {
		// So is a compaction the agent runs on its own, which it can start
		// before it writes anything.
		output = true
	}
	cycle := c.cycle
	opened := false
	if cycle == nil {
		if !output || c.closed {
			return nil, false
		}
		cycle = &providerCycle{id: providerCyclePrefix + uuid.NewString(), items: map[string]bool{}, tools: map[string]bool{}}
		c.cycle = cycle
		opened = !c.providerReportsState
	}
	cycle.timerGen++
	if cycle.timer != nil {
		cycle.timer.Stop()
		cycle.timer = nil
	}
	cycle.lastSeen = time.Now()
	if update.ToolCall != nil {
		cycle.tools[string(update.ToolCall.ToolCallId)] = true
	} else if update.ToolCallUpdate != nil && output {
		cycle.tools[string(update.ToolCallUpdate.ToolCallId)] = true
	}
	return cycle, opened
}

type itemKind int

const (
	itemMessage itemKind = iota
	itemThought
	itemNested // a subagent's message
)

// appendItemText adds a delta to a streamed item and reports whether the item
// already had text. A cycle's item is tracked in the same hold of mu, so the
// cycle's detach sees the item with all of its text or not at all. If a turn
// detached the cycle after this update entered it, the delta goes on the
// cycle's settled text, never in the turn's maps, and late returns the item to
// complete again after the delta is emitted: the turn's settle may already
// have completed it without the delta.
func (c *conversation) appendItemText(cycle *providerCycle, kind itemKind, id, parentID, delta string) (existed bool, late *settledCycle) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cycle != nil && c.cycle != cycle {
		return cycle.settled.append(kind, id, parentID, delta)
	}
	if cycle != nil {
		cycle.items[id] = true
	}
	switch kind {
	case itemMessage:
		_, existed = c.messages[id]
		c.messages[id] += delta
	case itemThought:
		_, existed = c.thoughts[id]
		c.thoughts[id] += delta
	case itemNested:
		var item nestedMessageState
		item, existed = c.nestedMessages[id]
		item.text += delta
		item.parentID = parentID
		c.nestedMessages[id] = item
	}
	return existed, nil
}

// append adds a late delta to a detached cycle's item and returns that item
// alone, with its whole text, to settle again.
func (s *settledCycle) append(kind itemKind, id, parentID, delta string) (bool, *settledCycle) {
	late := newSettledCycle()
	existed := false
	switch kind {
	case itemMessage:
		_, existed = s.messages[id]
		s.messages[id] += delta
		late.messages[id] = s.messages[id]
	case itemThought:
		_, existed = s.thoughts[id]
		s.thoughts[id] += delta
		late.thoughts[id] = s.thoughts[id]
	case itemNested:
		var item nestedMessageState
		item, existed = s.nested[id]
		item.text += delta
		item.parentID = parentID
		s.nested[id] = item
		late.nested[id] = item
	}
	return existed, late
}

func newSettledCycle() *settledCycle {
	return &settledCycle{messages: map[string]string{}, thoughts: map[string]string{}, nested: map[string]nestedMessageState{}}
}

// armProviderCycle schedules the end of a quiet cycle once none of its tools is
// open. A tool still running holds the cycle; its completion re-arms it.
func (c *conversation) armProviderCycle(cycle *providerCycle, update acpsdk.SessionUpdate) {
	if cycle == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cycle != cycle || c.providerReportsState || c.closed {
		return
	}
	for id := range cycle.tools {
		if tool := c.tools[id]; tool != nil && toolStatusOpen(tool.status) {
			return
		}
	}
	if compaction := c.providerCompaction; compaction != nil && compaction.turnID == cycle.id && !compaction.finished {
		// A compaction under way holds the cycle too; its outcome re-arms it.
		return
	}
	// Only a usage_update that is the latest update ends the cycle early. One
	// before a tool call closed the response that asked for the tool; once the
	// tool completes the agent answers its result in a new response, which
	// ends with its own usage_update, so a tool completion waits the fallback.
	wait := providerCycleQuietWait
	if update.UsageUpdate != nil {
		wait = providerCycleSettleGrace
	}
	gen := cycle.timerGen
	cycle.timer = time.AfterFunc(wait, func() { c.endQuietProviderCycle(cycle, gen) })
}

func (c *conversation) endQuietProviderCycle(cycle *providerCycle, gen int) {
	// Held through the idle report: a cycle opening meanwhile waits to report
	// active until this one has reported idle.
	c.cycleStateMu.Lock()
	defer c.cycleStateMu.Unlock()
	c.mu.Lock()
	if c.cycle != cycle || cycle.timerGen != gen || c.closed {
		c.mu.Unlock()
		return
	}
	c.detachProviderCycleLocked()
	c.mu.Unlock()
	c.emitEndedCycle(cycle)
	c.emit(ports.ChatEvent{Kind: ports.ChatEventProviderState, ThreadState: &ports.ChatThreadState{Status: domain.ThreadStatusIdle}})
}

// endProviderCycle settles an open cycle now: the agent said it is idle.
func (c *conversation) endProviderCycle() {
	c.cycleStateMu.Lock()
	defer c.cycleStateMu.Unlock()
	c.mu.Lock()
	cycle := c.detachProviderCycleLocked()
	c.mu.Unlock()
	c.emitEndedCycle(cycle)
}

// settleEndedCycle completes the items of a cycle a turn ended.
func (c *conversation) settleEndedCycle(cycle *providerCycle) {
	if cycle == nil {
		return
	}
	c.cycleStateMu.Lock()
	defer c.cycleStateMu.Unlock()
	c.emitEndedCycle(cycle)
}

// emitEndedCycle completes a detached cycle's items with their text as it is
// now. The caller holds cycleStateMu, which an update holds from changing a
// cycle item through emitting its events, so the items' events reach the
// consumer in the order the items changed and this completion comes after
// every delta already applied to them.
func (c *conversation) emitEndedCycle(cycle *providerCycle) {
	if cycle == nil {
		return
	}
	c.mu.Lock()
	settled := &settledCycle{messages: maps.Clone(cycle.settled.messages),
		thoughts: maps.Clone(cycle.settled.thoughts), nested: maps.Clone(cycle.settled.nested)}
	// A compaction the cycle left open settles with it, as one a turn left open
	// does when the turn ends.
	compaction := c.providerCompaction
	if compaction != nil && compaction.turnID != cycle.id {
		compaction = nil
	}
	if compaction != nil && !compaction.finished {
		c.providerCompaction = nil
	}
	if c.settledCompaction != nil && c.settledCompaction.turnID == cycle.id {
		c.settledCompaction = nil
	}
	c.mu.Unlock()
	c.emitSettledCycle(settled)
	switch {
	case compaction == nil:
	case compaction.finished:
		c.settleFinishedCompaction(c.emit)
	default:
		c.settleUnreportedCompaction(compaction, c.emit)
	}
}

// detachProviderCycleLocked closes the open cycle and moves what it left open
// onto it, out of the maps a turn settles, and returns it for emitEndedCycle.
func (c *conversation) detachProviderCycleLocked() *providerCycle {
	cycle := c.cycle
	if cycle == nil {
		return nil
	}
	c.cycle = nil
	cycle.timerGen++
	if cycle.timer != nil {
		cycle.timer.Stop()
		cycle.timer = nil
	}
	settled := newSettledCycle()
	for id := range cycle.items {
		if text, ok := c.messages[id]; ok {
			settled.messages[id] = text
			delete(c.messages, id)
		}
		if text, ok := c.thoughts[id]; ok {
			settled.thoughts[id] = text
			delete(c.thoughts, id)
		}
		if item, ok := c.nestedMessages[id]; ok {
			settled.nested[id] = item
			delete(c.nestedMessages, id)
		}
	}
	cycle.settled = settled
	return cycle
}

// emitSettledCycle completes a cycle's items, turn-less like their deltas.
// Its tools settle by their own updates: one still running when the next turn
// starts keeps running and reports its end there.
func (c *conversation) emitSettledCycle(settled *settledCycle) {
	if settled == nil {
		return
	}
	for _, id := range sortedKeys(settled.messages) {
		c.emit(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderItemID: id, Text: settled.messages[id]})
	}
	for _, id := range sortedKeys(settled.thoughts) {
		c.emit(ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderItemID: id,
			ActivityKind: domain.ActivityKindReasoning, ActivityStatus: domain.ActivityStatusCompleted,
			Summary: "Reasoning", Text: settled.thoughts[id]})
	}
	for _, id := range sortedKeys(settled.nested) {
		item := settled.nested[id]
		detail, _ := json.Marshal(map[string]any{"parentProviderItemId": item.parentID, "nestedAgent": true})
		c.emit(ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderItemID: id,
			ActivityKind: domain.ActivityKindMCPTool, ActivityStatus: domain.ActivityStatusCompleted,
			Summary: "Subagent response", Text: item.text, Detail: detail})
	}
}

// providerCycleActive reports a cycle the agent is still running, bounded like
// an event-derived background task so a cycle the agent never ends cannot keep
// the session awake forever.
func (c *conversation) providerCycleActive(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cycle != nil && now.Sub(c.cycle.lastSeen) < staleBackgroundTask
}

// keepToolsOutlivingTurn tracks, for an agent whose commands outlive the turn
// that ran them (Config.ToolsOutliveTurn), each command still running when the
// prompt ended normally as a background task, and returns their ids so the
// turn's settlement leaves them running. Their completion arrives later as an
// ordinary tool update, which settles both the row and the task.
func (c *conversation) keepToolsOutlivingTurn(interrupted bool, resp acpsdk.PromptResponse, err error) map[string]bool {
	if !c.toolsOutliveTurn || interrupted || err != nil || resp.StopReason != acpsdk.StopReasonEndTurn {
		return nil
	}
	// The tracker is updated under mu, the lock a completion's status update
	// takes: a completion merged first leaves the tool out here, and one merged
	// after finds the task to finish. Updating after mu was released could
	// record a tool that had just completed as running.
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	var kept map[string]bool
	for _, tool := range c.tools {
		if tool.status != acpsdk.ToolCallStatusInProgress {
			continue
		}
		if kept == nil {
			kept = map[string]bool{}
		}
		kept[tool.id] = true
		input := backgroundObject(tool.rawInput)
		kind, command := "other", ""
		if tool.kind == acpsdk.ToolKindExecute {
			// Antigravity's run_command names its command line CommandLine.
			kind, command = "shell", stringField(input, "command", "cmd", "CommandLine")
		}
		description := stringField(input, "description", "command", "prompt")
		if description == "" {
			description = tool.title
		}
		c.backgroundObserved.Update(ports.ChatBackgroundTask{ID: tool.id, RunID: tool.id, Kind: kind,
			Description: description, Command: command, State: "running", StartedAt: &now}, now)
	}
	return kept
}
