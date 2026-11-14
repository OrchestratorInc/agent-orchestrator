package qwenacp

import (
	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

const (
	midTurnDrainMethod     = "craft/drainMidTurnQueue"
	midTurnSendNowMethod   = "craft/midTurnSendNow"
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

func drainResponse(texts []string) any {
	return map[string]any{"messages": texts, "hasQueuedPrompt": false}
}
