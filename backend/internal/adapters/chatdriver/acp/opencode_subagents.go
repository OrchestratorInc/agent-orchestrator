package acp

import (
	"encoding/json"
	"regexp"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// OpenCode subagents, in the shape AO's chat already reads for Claude's.
//
// OpenCode 2 launches a subagent with its `subagent` tool, and the child always
// runs in the background, in a session of its own: the call completes as soon as
// the child is queued, and only its rawOutput.metadata.sessionID names the child.
// Because AO sets the opencode/child-session-updates capability for OpenCode 2
// (clientCapabilityMeta), the agent sends the child's work as
// opencode/session/child_update notifications: each update the child makes, and
// its status as it starts and ends, also after the parent's turn is over. Nothing
// on those names the launch, so a child's updates wait until the launch that
// started it completes.
//
// OpenCode 1 launches with `task`, runs the child inline, and sends none of the
// child's events over ACP. The call's result wraps the child's report in a
// <task_result> element.
//
// The renderer groups a launch's steps by detail.parentProviderItemId and takes a
// SubagentHandback step as the child's report (subagent-team.ts). This file writes
// exactly that, so the timeline needs no OpenCode-specific code.

const (
	openCodeChildUpdatesCapability = "opencode/child-session-updates"
	openCodeChildUpdateMethod      = "opencode/session/child_update"
	// openCodeSubagentTool is OpenCode 2's launch, named as the renderer lists it.
	openCodeSubagentTool = "subagent"
	// subagentHandbackTool marks the child's report to its launch. It is the name
	// the renderer already reads for Claude's hand-back.
	subagentHandbackTool = "SubagentHandback"
	// maxHeldChildUpdates caps what waits for a launch to name its child. The
	// launch completes as soon as the child is queued, so the wait is short;
	// past the cap, everything held goes out unfiled rather than piling up.
	maxHeldChildUpdates = 2000
)

// openCodeChildUpdate is one opencode/session/child_update notification.
type openCodeChildUpdate struct {
	RootSessionID  string          `json:"rootSessionId"`
	ChildSessionID string          `json:"childSessionId"`
	Title          string          `json:"title"`
	Type           string          `json:"type"`
	Update         json.RawMessage `json:"update"`
	Status         string          `json:"status"`
	Error          *struct {
		Message string `json:"message"`
	} `json:"error"`
	Meta map[string]any `json:"_meta"`
}

// openCodeSubagents is what the conversation knows about OpenCode 2 children.
// It outlives turns, as the children do, and is guarded by conversation.mu.
type openCodeSubagents struct {
	// launchTurn is the provider turn each launch was made in. A child's edits
	// count in that turn's diff while it runs. A launch a child made takes the
	// turn of the child's own launch.
	launchTurn map[string]string
	// inflight holds launches that started but have not named their child.
	inflight map[string]struct{}
	// launchOf maps a child session to the launch that started it, or that last
	// sent it a follow-up. awaiting lists every launch that sent the child work
	// since it last reported, which all get its report.
	launchOf map[string]string
	awaiting map[string][]string

	// held keeps a child's updates, in order, until a launch names it.
	held      map[string][]openCodeChildUpdate
	heldOrder []string
	heldCount int

	// tools are the children's tool calls, kept apart from the turn's own so
	// the end of the parent's turn does not settle a child's work.
	tools map[string]*toolState
	// open is each child's message that is still streaming, and lastSaid the
	// text of the latest one it finished, which becomes its report.
	open     map[string]childMessage
	lastSaid map[string]string
}

// childMessage is what a child is saying, and the launch it was saying it for.
type childMessage struct{ id, text, launch string }

func (c *conversation) subagentsLocked() *openCodeSubagents {
	if c.subagents == nil {
		c.subagents = &openCodeSubagents{
			launchTurn: map[string]string{},
			inflight:   map[string]struct{}{},
			launchOf:   map[string]string{},
			awaiting:   map[string][]string{},
			held:       map[string][]openCodeChildUpdate{},
			tools:      map[string]*toolState{},
			open:       map[string]childMessage{},
			lastSaid:   map[string]string{},
		}
	}
	return c.subagents
}

// openCodeLaunchDetail marks an OpenCode launch in its activity detail.
func openCodeLaunchDetail(harness domain.AgentHarness, tool *toolState, detail map[string]any) {
	switch harness {
	case domain.HarnessOpenCodeV2:
		if !isOpenCodeSubagentLaunch(tool) {
			return
		}
		detail["providerToolName"] = openCodeSubagentTool
		// The launch settles as soon as the child starts, so only the child's
		// hand-back says it finished.
		detail["background"] = true
		if agent := stringField(tool.rawInput, "agent"); agent != "" {
			detail["subagentType"] = agent
		}
	case domain.HarnessOpenCode:
		if isOpenCodeTask(tool) && openCodeTaskState(tool) == "running" {
			// A task sent to the background reports back as a later prompt, not here.
			detail["background"] = true
		}
	}
}

// isOpenCodeSubagentLaunch reports OpenCode 2's subagent call. ACP carries no tool
// name, so it is recognised by its kind and input: an agent and a prompt.
func isOpenCodeSubagentLaunch(tool *toolState) bool {
	return tool.kind == acpsdk.ToolKindThink &&
		stringField(tool.rawInput, "agent") != "" && stringField(tool.rawInput, "prompt") != ""
}

// isOpenCodeTask reports OpenCode 1's task call, which the renderer already
// recognises by its subagent_type.
func isOpenCodeTask(tool *toolState) bool {
	return tool.kind == acpsdk.ToolKindThink && stringField(tool.rawInput, "subagent_type") != ""
}

var (
	openCodeTaskStatePattern  = regexp.MustCompile(`<task\b[^>]*\bstate="([a-z]+)"`)
	openCodeTaskResultPattern = regexp.MustCompile(`(?s)<task_result>(.*?)</task_result>`)
)

func openCodeTaskState(tool *toolState) string {
	match := openCodeTaskStatePattern.FindStringSubmatch(toolOutputText(tool.rawOutput))
	if match == nil {
		return ""
	}
	return match[1]
}

// observeSubagentLaunch follows a launch as its tool call changes. It runs after
// the launch's own event is out, so what it releases follows the launch.
func (c *conversation) observeSubagentLaunch(turnID string, tool *toolState, emit func(ports.ChatEvent)) {
	switch c.harness {
	case domain.HarnessOpenCode:
		// The child's report is the task's result, without the wrapper meant for
		// the model.
		if tool.status != acpsdk.ToolCallStatusCompleted || !isOpenCodeTask(tool) || openCodeTaskState(tool) == "running" {
			return
		}
		match := openCodeTaskResultPattern.FindStringSubmatch(toolOutputText(tool.rawOutput))
		if match == nil {
			return
		}
		emit(c.handbackEvent(turnID, tool.id, domain.ActivityStatusCompleted, strings.TrimSpace(match[1])))
	case domain.HarnessOpenCodeV2:
		if !isOpenCodeSubagentLaunch(tool) {
			return
		}
		c.mu.Lock()
		s := c.subagentsLocked()
		if _, known := s.launchTurn[tool.id]; !known {
			s.launchTurn[tool.id] = turnID
		}
		// The result names the child, a new one or the one a follow-up went to. Only
		// an accepted call moves the child's work to it: a rejected follow-up names
		// its child in the input too, but the child stays with the launch it has.
		child := ""
		if tool.status == acpsdk.ToolCallStatusCompleted {
			child = stringField(nestedMap(asMap(tool.rawOutput), "metadata"), "sessionID")
		}
		switch {
		case child != "":
			if s.launchOf[child] != tool.id {
				s.awaiting[child] = append(s.awaiting[child], tool.id)
			}
			s.launchOf[child] = tool.id
			delete(s.inflight, tool.id)
		case toolTerminal(tool.status):
			delete(s.inflight, tool.id)
		default:
			s.inflight[tool.id] = struct{}{}
		}
		released := s.release(false)
		c.mu.Unlock()
		for _, update := range released {
			c.applyChildUpdate(update)
		}
	}
}

// settleSubagentLaunches stops counting a settled turn's unnamed launches as in
// flight: nothing will name their child now, so what waits on them goes out
// unfiled.
func (c *conversation) settleSubagentLaunches(turnID string) {
	c.mu.Lock()
	s := c.subagents
	if s == nil {
		c.mu.Unlock()
		return
	}
	for launch := range s.inflight {
		if s.launchTurn[launch] == turnID {
			delete(s.inflight, launch)
		}
	}
	released := s.release(false)
	c.mu.Unlock()
	for _, update := range released {
		c.applyChildUpdate(update)
	}
}

// release returns held updates whose child a launch has now named, in the order
// they came. Once no launch is in flight, nothing can name the rest, so they are
// returned too, to go out unfiled; with all set, everything held is.
func (s *openCodeSubagents) release(all bool) []openCodeChildUpdate {
	var out []openCodeChildUpdate
	kept := s.heldOrder[:0]
	for _, child := range s.heldOrder {
		if _, placed := s.launchOf[child]; !all && !placed && len(s.inflight) > 0 {
			kept = append(kept, child)
			continue
		}
		out = append(out, s.held[child]...)
		s.heldCount -= len(s.held[child])
		delete(s.held, child)
	}
	s.heldOrder = kept
	return out
}

// handleOpenCodeChildUpdate takes one child update off the wire, holding it while
// a launch that may name its child is still running.
func (c *conversation) handleOpenCodeChildUpdate(params json.RawMessage) error {
	var update openCodeChildUpdate
	if err := json.Unmarshal(params, &update); err != nil {
		return err
	}
	c.mu.Lock()
	sessionID := c.sessionID
	if update.ChildSessionID == "" || (sessionID != "" && update.RootSessionID != sessionID) {
		c.mu.Unlock()
		return nil
	}
	s := c.subagentsLocked()
	_, placed := s.launchOf[update.ChildSessionID]
	_, waiting := s.held[update.ChildSessionID]
	if placed || (len(s.inflight) == 0 && !waiting) {
		c.mu.Unlock()
		c.applyChildUpdate(update)
		return nil
	}
	if s.heldCount >= maxHeldChildUpdates {
		released := append(s.release(true), update)
		c.mu.Unlock()
		for _, held := range released {
			c.applyChildUpdate(held)
		}
		return nil
	}
	if !waiting {
		s.heldOrder = append(s.heldOrder, update.ChildSessionID)
	}
	s.held[update.ChildSessionID] = append(s.held[update.ChildSessionID], update)
	s.heldCount++
	c.mu.Unlock()
	return nil
}

// applyChildUpdate files one child update under its launch. An update for a
// child no launch named goes out as the agent would have sent it without the
// capability: as an update of the parent's session.
func (c *conversation) applyChildUpdate(update openCodeChildUpdate) {
	c.mu.Lock()
	s := c.subagentsLocked()
	launch, placed := s.launchOf[update.ChildSessionID]
	c.mu.Unlock()
	if update.Type == "status" {
		if placed {
			c.childEnded(update, launch)
		}
		return
	}
	if update.Type != "update" || len(update.Update) == 0 {
		return
	}
	var sessionUpdate acpsdk.SessionUpdate
	if err := json.Unmarshal(update.Update, &sessionUpdate); err != nil {
		c.log.Warn("ignored malformed OpenCode child update", "child", update.ChildSessionID, "err", err)
		return
	}
	if !placed {
		if err := c.processUpdate(acpsdk.SessionNotification{
			SessionId: acpsdk.SessionId(update.RootSessionID), Update: sessionUpdate, Meta: update.Meta,
		}); err != nil {
			c.log.Warn("failed to handle unfiled OpenCode child update", "child", update.ChildSessionID, "err", err)
		}
		return
	}
	c.childStep(update, sessionUpdate, launch)
}

// childStep turns what a child did into a step under its launch. Only its tool
// calls and what it says are steps: its plan, usage and session details describe
// the child's session, and applied here they would replace the parent's. Its
// reasoning is left out, as Claude's subagent reasoning is.
//
// A child's steps carry no turn. The child runs on its own clock, and filed on
// the parent's turn, a step still running when that turn ends would be settled
// with it. The renderer reads a child's steps across the whole conversation.
func (c *conversation) childStep(update openCodeChildUpdate, sessionUpdate acpsdk.SessionUpdate, launch string) {
	emit := c.sourceEmitter(stableACPEventID(update.Meta))
	child := update.ChildSessionID
	switch {
	case sessionUpdate.ToolCall != nil, sessionUpdate.ToolCallUpdate != nil:
		c.mu.Lock()
		s := c.subagentsLocked()
		var id string
		if sessionUpdate.ToolCall != nil {
			id = string(sessionUpdate.ToolCall.ToolCallId)
			s.tools[id] = newToolState(sessionUpdate.ToolCall)
		} else {
			id = string(sessionUpdate.ToolCallUpdate.ToolCallId)
			mergeToolState(s.tools, sessionUpdate.ToolCallUpdate)
		}
		stored := s.tools[id]
		if stored.parentID == "" {
			// A step stays with the launch it began under, even if a follow-up
			// takes the child before it ends.
			stored.parentID = launch
		}
		// OpenCode prefixes each of a child's tool titles with the child's title,
		// which the launch already shows.
		if update.Title != "" {
			stored.title = strings.TrimPrefix(stored.title, update.Title+": ")
		}
		snapshot := *stored
		tool := &snapshot
		if toolTerminal(tool.status) {
			delete(s.tools, id)
		}
		turn := s.launchTurn[stored.parentID]
		activeTurn := c.activeTurn
		c.mu.Unlock()
		emit(c.toolEvent("", tool, toolTerminal(tool.status)))
		if turn != "" && turn == activeTurn {
			// A turn's diff only counts edits made while it runs.
			c.emitDiffs(turn, tool.id, tool.content, emit)
		}
		// A child can start a child of its own.
		c.observeSubagentLaunch(turn, tool, emit)

	case sessionUpdate.AgentMessageChunk != nil:
		chunk := sessionUpdate.AgentMessageChunk
		delta := contentText(chunk.Content)
		if delta == "" {
			return
		}
		// A follow-up launch reuses the child's session, so the launch keeps its
		// messages apart.
		messageID := "subagent-message-" + launch + "-" + child
		if chunk.MessageId != nil && *chunk.MessageId != "" {
			messageID = *chunk.MessageId
		}
		id := c.providerItemID(messageID)
		c.mu.Lock()
		s := c.subagentsLocked()
		previous := s.open[child]
		started := previous.id != id
		message := previous
		if started {
			// A new message finishes the one before it.
			message = childMessage{id: id, launch: launch}
			if previous.id != "" {
				s.lastSaid[child] = strings.TrimSpace(previous.text)
			}
		}
		message.text += delta
		s.open[child] = message
		c.mu.Unlock()
		if started && previous.id != "" {
			emit(c.childMessageEvent(previous, true))
		}
		if started {
			emit(c.childMessageEvent(message, false))
		}
		emit(ports.ChatEvent{Kind: ports.ChatEventActivityText, ProviderItemID: id, Delta: delta})
	}
}

// childMessageEvent opens or completes one thing a child said.
func (c *conversation) childMessageEvent(message childMessage, completed bool) ports.ChatEvent {
	detail, _ := json.Marshal(map[string]any{"parentProviderItemId": c.providerItemID(message.launch), "nestedAgent": true})
	event := ports.ChatEvent{
		Kind: ports.ChatEventActivityStarted, ProviderItemID: message.id,
		ActivityKind: domain.ActivityKindMCPTool, ActivityStatus: domain.ActivityStatusRunning,
		Summary: "Subagent response", Detail: detail,
	}
	if completed {
		event.Kind = ports.ChatEventActivityCompleted
		event.ActivityStatus = domain.ActivityStatusCompleted
		event.Text = message.text
	}
	return event
}

// childEnded writes the child's report when its run ends: what it said last, or
// why it failed.
func (c *conversation) childEnded(update openCodeChildUpdate, launch string) {
	var status domain.ActivityStatus
	switch update.Status {
	case "completed":
		status = domain.ActivityStatusCompleted
	case "failed":
		status = domain.ActivityStatusFailed
	case "interrupted":
		status = domain.ActivityStatusCancelled
	default:
		return
	}
	emit := c.sourceEmitter(stableACPEventID(update.Meta))
	child := update.ChildSessionID
	c.mu.Lock()
	s := c.subagentsLocked()
	open, streaming := s.open[child]
	delete(s.open, child)
	report := s.lastSaid[child]
	if streaming {
		report = strings.TrimSpace(open.text)
	}
	delete(s.lastSaid, child)
	launches := s.awaiting[child]
	delete(s.awaiting, child)
	c.mu.Unlock()
	if len(launches) == 0 {
		launches = []string{launch}
	}
	if streaming {
		emit(c.childMessageEvent(open, true))
	}
	if status == domain.ActivityStatusFailed && update.Error != nil && strings.TrimSpace(update.Error.Message) != "" {
		report = strings.TrimSpace(update.Error.Message)
	}
	// A follow-up joins the child's run, so its outcome answers each launch that
	// is waiting on it, not only the latest.
	for _, waiting := range launches {
		emit(c.handbackEvent("", waiting, status, report))
	}
}

// handbackEvent is a child's report to the launch that started it.
func (c *conversation) handbackEvent(turn, launch string, status domain.ActivityStatus, message string) ports.ChatEvent {
	detail := map[string]any{
		"parentProviderItemId": c.providerItemID(launch),
		"providerToolName":     subagentHandbackTool,
	}
	if message != "" {
		detail["input"] = map[string]any{"message": message}
	}
	encoded, _ := json.Marshal(detail)
	return ports.ChatEvent{
		Kind: ports.ChatEventActivityCompleted, ProviderTurnID: turn,
		ProviderItemID: c.providerItemID("subagent-handback:" + launch),
		ActivityKind:   domain.ActivityKindMCPTool, ActivityStatus: status,
		Summary: subagentHandbackTool, Detail: encoded,
	}
}

func asMap(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func stringField(value any, key string) string {
	s, _ := asMap(value)[key].(string)
	return strings.TrimSpace(s)
}
