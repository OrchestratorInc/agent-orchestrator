package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
)

// Subagents an agent announces over ACP, in the shape AO's chat already reads
// for Claude's.
//
// An agent that implements the subagents extension (Cursor's `cursor-agent
// acp`) sends nothing of a child unless the client opts in with
// clientCapabilities._meta.subagents. With it, the agent announces each child
// on the parent's session:
//
//   - `subagent_spawned` names the child's own session (subagentSessionId) and
//     the tool call that launched it (_meta.<agent>.toolCallId);
//   - `subagent_state_update` says how it ended: completed, failed, cancelled,
//     or disconnected;
//
// and streams the child's own work as ordinary session/update frames under
// the child's session id. Neither update type is in the pinned SDK, which
// rejects an unknown discriminator, so both are read off the raw frame.
//
// This file files the child's tool calls and messages under its launch, on the
// parent's running turn: the agent's prompt returns only once that turn's
// children have settled. The child sends no report of its own, so the end of
// each run reports what the child said last.

// SpawnedSubagents is a binding's knowledge of subagents its agent announces
// over ACP. Launch is required.
type SpawnedSubagents struct {
	// Launch reports whether a tool call starts a subagent, and how it shows.
	Launch func(RecordedCall) (RecordedLaunch, bool)
}

// spawnedChild is one announced child session, guarded by conversation.mu.
type spawnedChild struct {
	// launch is the launch in RecordedUpdate.Launch form: the parent's tool
	// call id, or a child's tool item for a child a child started.
	launch string
	// message names the child's current message for an agent that sends none,
	// and messages counts them; said is the current message's text so far,
	// the report if the child stops there.
	message  string
	messages int
	said     string
}

const (
	subagentSpawned     = "subagent_spawned"
	subagentStateUpdate = "subagent_state_update"
)

// spawnedSubagentNotification handles the subagents extension's own update
// types. It reports whether the frame was one; one for a session this
// conversation does not hold is dropped.
func (c *conversation) spawnedSubagentNotification(raw json.RawMessage) bool {
	var notification struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			Type       string         `json:"sessionUpdate"`
			Child      string         `json:"subagentSessionId"`
			State      string         `json:"state"`
			ToolCallID string         `json:"toolCallId"`
			Meta       map[string]any `json:"_meta"`
		} `json:"update"`
	}
	if json.Unmarshal(raw, &notification) != nil {
		return false
	}
	update := notification.Update
	kind := strings.TrimPrefix(update.Type, "_")
	if kind != subagentSpawned && kind != subagentStateUpdate {
		return false
	}
	if update.Child == "" {
		return true
	}
	c.mu.Lock()
	parentChild := c.spawned[notification.SessionID]
	ours := notification.SessionID == c.sessionID || parentChild != nil
	if !ours {
		c.mu.Unlock()
		return true
	}
	if kind == subagentSpawned {
		toolCallID := update.ToolCallID
		if toolCallID == "" {
			toolCallID = metaToolCallID(update.Meta)
		}
		if toolCallID == "" || len(c.spawned) >= maxSpawnedChildren {
			c.mu.Unlock()
			return true
		}
		launch := toolCallID
		if parentChild != nil {
			// A child's launch is the child's own tool call, filed under it.
			launch = "subagent-tool:" + lengthPrefixedTuple(notification.SessionID, toolCallID)
		}
		if c.spawned == nil {
			c.spawned = map[string]*spawnedChild{}
		}
		if c.spawned[update.Child] == nil {
			c.spawned[update.Child] = &spawnedChild{launch: launch}
		}
		c.mu.Unlock()
		return true
	}
	child := c.spawned[update.Child]
	if child == nil {
		c.mu.Unlock()
		return true
	}
	status, ended := spawnedEndStatus(update.State)
	if !ended {
		c.mu.Unlock()
		return true
	}
	end := RecordedUpdate{Launch: child.launch, Child: update.Child, Run: update.Child, End: &RecordedEnd{
		ID: update.Child, Status: status, Report: strings.TrimSpace(child.said),
	}}
	if status != RecordedCompleted && end.End.Report == "" {
		end.End.Report = "The subagent " + update.State + "."
	}
	c.mu.Unlock()
	c.fileSpawnedUpdate(end)
	return true
}

// maxSpawnedChildren caps the children one conversation follows.
const maxSpawnedChildren = 256

// metaToolCallID finds the launching tool call in an agent's namespaced
// metadata, such as _meta.cursor.toolCallId.
func metaToolCallID(meta map[string]any) string {
	if id, _ := meta["toolCallId"].(string); id != "" {
		return id
	}
	for _, value := range meta {
		if nested, ok := value.(map[string]any); ok {
			if id, _ := nested["toolCallId"].(string); id != "" {
				return id
			}
		}
	}
	return ""
}

func spawnedEndStatus(state string) (RecordedEndStatus, bool) {
	switch state {
	case "completed":
		return RecordedCompleted, true
	case "cancelled":
		return RecordedCancelled, true
	case "failed", "disconnected":
		return RecordedFailed, true
	}
	return "", false
}

// routeSpawnedChild files a frame from an announced child's session under its
// launch, and reports whether it was one.
func (c *conversation) routeSpawnedChild(params acpsdk.SessionNotification) bool {
	session := string(params.SessionId)
	c.mu.Lock()
	child := c.spawned[session]
	if child == nil {
		c.mu.Unlock()
		return false
	}
	update := params.Update
	switch {
	case update.ToolCall != nil, update.ToolCallUpdate != nil:
		// A tool call ends what the child was saying; its next text is a new
		// message.
		child.message = ""
	case update.AgentMessageChunk != nil:
		chunk := *update.AgentMessageChunk
		id := ""
		if chunk.MessageId != nil {
			id = *chunk.MessageId
		}
		if id == "" {
			// No id from the agent: the message runs until the child's next tool
			// call.
			id = child.message
			if id == "" {
				child.messages++
				id = fmt.Sprintf("message-%d", child.messages)
			}
		}
		if id != child.message {
			child.said = ""
		}
		child.message = id
		child.said += contentText(chunk.Content)
		chunk.MessageId = &id
		update.AgentMessageChunk = &chunk
	}
	recorded := RecordedUpdate{Launch: child.launch, Child: session, Run: session, Update: &update}
	c.mu.Unlock()
	c.fileSpawnedUpdate(recorded)
	return true
}

// fileSpawnedUpdate files one child update on the running turn. Outside a turn
// (a history replay, or work after the turn settled) it is dropped: the
// agent's prompt holds the turn while its children work.
func (c *conversation) fileSpawnedUpdate(recorded RecordedUpdate) {
	c.mu.Lock()
	turnID := c.activeTurn
	c.ensureRecordFollowerLocked()
	follower := c.recordFollower
	c.mu.Unlock()
	if turnID == "" || follower == nil {
		return
	}
	follower.readMu <- struct{}{}
	defer func() { <-follower.readMu }()
	c.emitRecordedUpdate(context.Background(), turnID, follower, recorded)
}
