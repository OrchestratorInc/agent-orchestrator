package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Subagents that run as ACP sessions of their own, in the shape AO's chat
// already reads for Claude's.
//
// Some agents (Antigravity's `agy acp`) launch a subagent with an ordinary tool
// call that completes as soon as the child starts, and send nothing of the
// child's work on the parent's session: not its id, its steps or its report.
// The child is a session of the same agent, though, and session/load on the
// same connection streams it, history first and then live. The binding knows
// where the agent records which launch started which session (ChildSessions);
// this file finds the child once its launch completes, loads it, and files what
// the child does under the launch:
//
//   - its tool calls become steps with detail.parentProviderItemId and no turn,
//     because the child runs on its own clock and a step filed on the parent's
//     turn would be settled with it;
//   - what it says becomes nested "Subagent response" steps;
//   - its report to the parent becomes a SubagentHandback step.
//
// The renderer groups those by parentProviderItemId (subagent-team.ts), so the
// timeline needs no agent-specific code.

// ChildSessions is a binding's knowledge of subagents that run as sessions of
// their own. Every func is required.
type ChildSessions struct {
	// Launch reports whether a tool call starts a subagent, and how to show it.
	Launch func(ChildToolCall) (SubagentLaunch, bool)
	// Report returns the text of a child's tool call that reports to the
	// session that launched it, parentSession.
	Report func(call ChildToolCall, parentSession string) (string, bool)
	// Children lists the sessions that session has started, as the agent has
	// recorded them so far. env is the launch environment.
	Children func(env map[string]string, session string) []string
	// LaunchOf returns the session and the launch tool call that started
	// child, once the agent has recorded it.
	LaunchOf func(env map[string]string, child string) (parentSession, launchID string, ok bool)
}

// ChildToolCall is what ChildSessions reads of a tool call.
type ChildToolCall struct {
	Title    string
	Kind     acpsdk.ToolKind
	Status   acpsdk.ToolCallStatus
	RawInput any
}

// SubagentLaunch is how a launch shows: the tool name the renderer lists as a
// launch, a short title, the task, and the agent type (empty for the default).
type SubagentLaunch struct {
	ToolName, Title, Prompt, AgentType string
}

const (
	// childSessionHandbackTool marks the child's report to its launch, the name
	// the renderer already reads for Claude's hand-back.
	childSessionHandbackTool = "SubagentHandback"
	// childSessionFindTimeout bounds how long a completed launch waits for the
	// agent to record the session it started; the agent writes it at once.
	childSessionFindTimeout = 30 * time.Second
	childSessionFindEvery   = 250 * time.Millisecond
	childSessionLoadTimeout = 30 * time.Second
	// maxChildSessions caps the children one conversation follows.
	maxChildSessions = 64
)

// childSessionState is what the conversation knows about its children. It
// outlives turns, as the children do, and is guarded by conversation.mu.
type childSessionState struct {
	// finding holds launches whose child is being looked for, or was found.
	finding map[string]struct{}
	// launchOf maps a child session to its launch, and parentOf to the session
	// that made the launch: the conversation's, or another child's.
	launchOf map[string]string
	parentOf map[string]string
	// notChild holds sessions already looked up and found not to be ours.
	notChild map[string]struct{}
	// tools are the children's tool calls, kept apart from the turn's own so
	// the end of the parent's turn does not settle a child's work.
	tools map[string]*toolState
	// said is each child's message so far, and messages counts the messages a
	// child has sent since it was loaded, which names them stably across loads.
	said     map[string]*childSaying
	messages map[string]int
}

type childSaying struct{ id, text string }

func (c *conversation) childStateLocked() *childSessionState {
	if c.children == nil {
		c.children = &childSessionState{
			finding:  map[string]struct{}{},
			launchOf: map[string]string{},
			parentOf: map[string]string{},
			notChild: map[string]struct{}{},
			tools:    map[string]*toolState{},
			said:     map[string]*childSaying{},
			messages: map[string]int{},
		}
	}
	return c.children
}

func childCallView(tool *toolState) ChildToolCall {
	return ChildToolCall{Title: tool.title, Kind: tool.kind, Status: tool.status, RawInput: tool.rawInput}
}

// decorateSubagentLaunch marks a launch in its activity detail: the renderer's
// launch name, the child's task as input, and background, because the launch
// settles as soon as the child starts and only the hand-back says it finished.
func (c *conversation) decorateSubagentLaunch(tool *toolState, detail map[string]any) {
	if c.childSessions == nil {
		return
	}
	launch, ok := c.childSessions.Launch(childCallView(tool))
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

// observeChildLaunch starts looking for the session a completed launch started.
// session is the session that made the launch.
func (c *conversation) observeChildLaunch(tool *toolState, session string) {
	if c.childSessions == nil || tool.status != acpsdk.ToolCallStatusCompleted || session == "" {
		return
	}
	if _, ok := c.childSessions.Launch(childCallView(tool)); !ok {
		return
	}
	c.mu.Lock()
	s := c.childStateLocked()
	if _, seen := s.finding[tool.id]; seen || len(s.finding) >= maxChildSessions {
		c.mu.Unlock()
		return
	}
	s.finding[tool.id] = struct{}{}
	c.mu.Unlock()
	go c.findChildSession(session, tool.id)
}

// findChildSession waits for the agent to record the session launchID started,
// then loads it so its work streams here.
func (c *conversation) findChildSession(session, launchID string) {
	deadline := time.NewTimer(childSessionFindTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(childSessionFindEvery)
	defer tick.Stop()
	for {
		for _, child := range c.childSessions.Children(c.childEnv, session) {
			c.mu.Lock()
			_, known := c.childStateLocked().launchOf[child]
			c.mu.Unlock()
			if known {
				continue
			}
			parent, launch, ok := c.childSessions.LaunchOf(c.childEnv, child)
			if !ok || parent != session || launch != launchID {
				continue
			}
			c.mu.Lock()
			s := c.childStateLocked()
			s.launchOf[child] = launchID
			s.parentOf[child] = session
			delete(s.notChild, child)
			c.mu.Unlock()
			c.loadChildSession(child)
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			c.log.Debug("subagent launch named no session", "launch", launchID)
			return
		case <-c.closing:
			return
		case <-c.conn.Done():
			return
		}
	}
}

// loadChildSession opens the child's session beside the conversation's. The
// agent then replays what the child did and streams the rest.
func (c *conversation) loadChildSession(child string) {
	ctx, cancel := context.WithTimeout(context.Background(), childSessionLoadTimeout)
	defer cancel()
	go func() {
		select {
		case <-c.closing:
			cancel()
		case <-ctx.Done():
		}
	}()
	_, err := c.conn.LoadSession(ctx, acpsdk.LoadSessionRequest{
		SessionId: acpsdk.SessionId(child), Cwd: c.childCwd, McpServers: []acpsdk.McpServer{},
		Meta: map[string]any{persistenthost.ACPChildSessionMetaKey: true},
	})
	if err != nil {
		c.log.Warn("could not follow subagent session", "child", child, "err", err)
	}
}

// routeChildUpdate files an update of a child session under its launch and
// reports whether it did. An update of an unknown session is a child when the
// agent recorded a launch of ours for it: one loaded by an earlier daemon
// attachment, still streaming through the persistent host.
func (c *conversation) routeChildUpdate(params acpsdk.SessionNotification) bool {
	if c.childSessions == nil {
		return false
	}
	child := string(params.SessionId)
	c.mu.Lock()
	if c.sessionID == "" || child == c.sessionID {
		c.mu.Unlock()
		return false
	}
	s := c.childStateLocked()
	launch, known := s.launchOf[child]
	_, notChild := s.notChild[child]
	c.mu.Unlock()
	if !known {
		if notChild {
			return false
		}
		parent, launchID, ok := c.childSessions.LaunchOf(c.childEnv, child)
		c.mu.Lock()
		s = c.childStateLocked()
		_, parentKnown := s.launchOf[parent]
		if !ok || (parent != c.sessionID && !parentKnown) || len(s.launchOf) >= maxChildSessions {
			s.notChild[child] = struct{}{}
			c.mu.Unlock()
			return false
		}
		s.launchOf[child], s.parentOf[child] = launchID, parent
		s.finding[launchID] = struct{}{}
		launch = launchID
		c.mu.Unlock()
	}
	c.childUpdate(child, launch, params)
	return true
}

// childUpdate turns what a child did into steps under its launch. Only its tool
// calls and what it says are steps: its plan, usage, modes, commands and
// session details describe the child's session, and applied here they would
// replace the parent's. Its reasoning is left out, as Claude's subagent
// reasoning is.
func (c *conversation) childUpdate(child, launch string, params acpsdk.SessionNotification) {
	emit := c.sourceEmitter(stableACPEventID(params.Meta))
	update := params.Update
	switch {
	case update.ToolCall != nil, update.ToolCallUpdate != nil:
		c.mu.Lock()
		s := c.childStateLocked()
		var stored *toolState
		if update.ToolCall != nil {
			stored = newToolState(update.ToolCall)
			s.tools[stored.id] = stored
		} else {
			stored = mergeToolState(s.tools, update.ToolCallUpdate)
		}
		snapshot := *stored
		tool := &snapshot
		if toolTerminal(tool.status) {
			delete(s.tools, tool.id)
		}
		parent := s.parentOf[child]
		// A tool call ends what the child was saying; its next text is a new message.
		delete(s.said, child)
		c.mu.Unlock()
		if report, ok := c.childSessions.Report(childCallView(tool), parent); ok {
			if tool.status == acpsdk.ToolCallStatusCompleted {
				emit(c.childHandbackEvent(launch, report))
			}
			return
		}
		emit(withParentItem(c.toolEvent("", tool, toolTerminal(tool.status)), c.providerItemID(launch)))
		// A child can start a child of its own.
		c.observeChildLaunch(tool, child)

	case update.AgentMessageChunk != nil:
		delta := contentText(update.AgentMessageChunk.Content)
		if delta == "" {
			return
		}
		c.mu.Lock()
		s := c.childStateLocked()
		said := s.said[child]
		if said == nil {
			s.messages[child]++
			said = &childSaying{id: c.providerItemID(fmt.Sprintf("subagent-message:%s:%d", child, s.messages[child]))}
			s.said[child] = said
		}
		said.text += delta
		text := strings.TrimSpace(said.text)
		id := said.id
		c.mu.Unlock()
		// Nothing on the wire says a child's message is over, so each piece settles
		// it with the text so far: a message the child stops at stays complete.
		detail, _ := json.Marshal(map[string]any{"parentProviderItemId": c.providerItemID(launch), "nestedAgent": true})
		emit(ports.ChatEvent{
			Kind: ports.ChatEventActivityCompleted, ProviderItemID: id,
			ActivityKind: domain.ActivityKindMCPTool, ActivityStatus: domain.ActivityStatusCompleted,
			Summary: "Subagent response", Detail: detail, Text: text,
		})
	}
}

// childHandbackEvent is a child's report to the launch that started it. A
// child that reports again replaces its report, so the latest one stands.
func (c *conversation) childHandbackEvent(launch, message string) ports.ChatEvent {
	detail := map[string]any{
		"parentProviderItemId": c.providerItemID(launch),
		"providerToolName":     childSessionHandbackTool,
	}
	if message = strings.TrimSpace(message); message != "" {
		detail["input"] = map[string]any{"message": message}
	}
	encoded, _ := json.Marshal(detail)
	return ports.ChatEvent{
		Kind:           ports.ChatEventActivityCompleted,
		ProviderItemID: c.providerItemID("subagent-handback:" + launch),
		ActivityKind:   domain.ActivityKindMCPTool, ActivityStatus: domain.ActivityStatusCompleted,
		Summary: childSessionHandbackTool, Detail: encoded,
	}
}

// withParentItem files a step under the launch whose provider item id is parent.
func withParentItem(event ports.ChatEvent, parent string) ports.ChatEvent {
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
