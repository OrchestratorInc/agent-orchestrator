package acp

import (
	"encoding/json"
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

type providerCycle struct {
	id       string
	items    map[string]bool // message, reasoning and subagent-message ids
	tools    map[string]bool // tool calls started in this cycle
	timerGen int
	timer    *time.Timer
	lastSeen time.Time
}

// settledCycle is what a cycle left open, copied while holding mu.
type settledCycle struct {
	messages map[string]string
	thoughts map[string]string
	nested   map[string]nestedMessageState
}

// enterProviderCycle returns the cycle a turn-less update belongs to, opening
// one for agent output. Updates that are not output (usage, a late update for
// an earlier turn's tool) join an open cycle but never open one. It stops the
// cycle's settle timer: the update re-arms it once it has been applied.
func (c *conversation) enterProviderCycle(update acpsdk.SessionUpdate) (*providerCycle, bool) {
	output := update.AgentMessageChunk != nil || update.AgentThoughtChunk != nil ||
		update.ToolCall != nil || update.Plan != nil
	c.mu.Lock()
	defer c.mu.Unlock()
	if update.ToolCallUpdate != nil && c.tools[string(update.ToolCallUpdate.ToolCallId)] == nil {
		// A tool AO never saw start is output of the cycle it belongs to.
		output = true
	}
	cycle := c.cycle
	opened := false
	if cycle == nil {
		if !output || c.closed {
			return nil, false
		}
		cycle = &providerCycle{id: "cycle-" + uuid.NewString(), items: map[string]bool{}, tools: map[string]bool{}}
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

func (c *conversation) trackCycleItem(cycle *providerCycle, id string) {
	if cycle == nil {
		return
	}
	c.mu.Lock()
	cycle.items[id] = true
	c.mu.Unlock()
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
	c.mu.Lock()
	if c.cycle != cycle || cycle.timerGen != gen || c.closed {
		c.mu.Unlock()
		return
	}
	settled := c.detachProviderCycleLocked()
	c.mu.Unlock()
	c.emitSettledCycle(settled)
	c.emit(ports.ChatEvent{Kind: ports.ChatEventProviderState, ThreadState: &ports.ChatThreadState{Status: domain.ThreadStatusIdle}})
}

// endProviderCycle settles an open cycle now: the agent said it is idle.
func (c *conversation) endProviderCycle() {
	c.mu.Lock()
	settled := c.detachProviderCycleLocked()
	c.mu.Unlock()
	c.emitSettledCycle(settled)
}

// detachProviderCycleLocked closes the open cycle and copies what it left
// open, removing those items so a later settle does not repeat them. The
// caller emits the copy once mu is released.
func (c *conversation) detachProviderCycleLocked() *settledCycle {
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
	settled := &settledCycle{messages: map[string]string{}, thoughts: map[string]string{}, nested: map[string]nestedMessageState{}}
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
	return settled
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
	c.mu.Lock()
	tools := make([]toolState, 0, len(c.tools))
	for _, tool := range c.tools {
		if tool.status == acpsdk.ToolCallStatusInProgress {
			tools = append(tools, *tool)
		}
	}
	c.mu.Unlock()
	if len(tools) == 0 {
		return nil
	}
	now := time.Now()
	kept := make(map[string]bool, len(tools))
	for _, tool := range tools {
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
