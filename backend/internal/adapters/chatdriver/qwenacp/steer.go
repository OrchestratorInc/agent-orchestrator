package qwenacp

import (
	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// Qwen Code takes mid-turn guidance by asking the client for it. At its step
// boundaries (after a batch of tool calls, and before it ends a response) its
// ACP agent calls craft/drainMidTurnQueue and folds whatever comes back into
// the running turn as "[User message received during tool execution]: ...".
// It advertises nothing in initialize, so support is read from its version.
const (
	midTurnDrainMethod = "craft/drainMidTurnQueue"
	// midTurnSendNowMethod asks Qwen to collect at its next opportunity, which
	// can cut a streaming response short. Qwen 0.25 and newer answer it; older
	// builds refuse it and simply collect at the next boundary.
	midTurnSendNowMethod = "craft/midTurnSendNow"
	// minimumSteeringVersion is the first Qwen Code release whose ACP agent
	// drains the mid-turn queue.
	minimumSteeringVersion = "0.18.0"
)

var steerDrain = &acpdriver.SteerDrain{
	Supported: steeringSupported,
	Method:    midTurnDrainMethod,
	Response:  drainResponse,
	Nudge:     midTurnSendNowMethod,
}

func steeringSupported(init acpsdk.InitializeResponse) bool {
	if init.AgentInfo == nil {
		return false
	}
	installed, ok := parseVersion(init.AgentInfo.Version)
	if !ok {
		return false
	}
	minimum, _ := parseVersion(minimumSteeringVersion)
	return !installed.less(minimum)
}

// drainResponse uses the plain-text messages shape, the only one Qwen 0.18
// reads. Qwen's todo stop guard asks for hasQueuedPrompt to decide whether to
// yield to a prompt the client has waiting. AO starts its queued turns itself
// after the turn ends, so it answers false and leaves that decision unchanged.
func drainResponse(texts []string) any {
	return map[string]any{"messages": texts, "hasQueuedPrompt": false}
}
