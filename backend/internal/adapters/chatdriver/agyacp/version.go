package agyacp

import (
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentversion"
)

// serverAgentName is the agentInfo.name Google's Antigravity ACP server reports.
const serverAgentName = "antigravity-acp"

// minimumServerVersion is the oldest server AO has verified end to end:
// session/request_permission with allow_once, default/auto_edit/yolo modes,
// session/load, and authenticate reusing an existing Google sign-in.
const minimumServerVersion = "1.3.0"

// cliServerAgentName is the agentInfo.name of the ACP server built into the
// agy CLI, started as `agy acp`.
const cliServerAgentName = "Antigravity ACP Server"

// minimumCLIACPVersion is the first agy CLI whose `agy acp` AO launches. Its
// `agy acp` steers a running turn when another prompt arrives from 1.5.0, but
// before 1.5.4 a turn hangs when a tool call needs a second approval, and a
// resumed session drops back to the default mode (1.5.4 changelog). AO
// launches older CLIs' separate server instead.
const minimumCLIACPVersion = "1.5.4"

// validateInitialize admits only Google's Antigravity ACP servers at a tested
// version: the separate server, which has no --version flag, or `agy acp`.
func validateInitialize(init acpsdk.InitializeResponse) error {
	if isCLIServer(init) {
		installed, ok := agentversion.ParsePrefix(init.AgentInfo.Version)
		if !ok || installed.Less(agentversion.MustParse(minimumCLIACPVersion)) {
			return fmt.Errorf("agy acp %q is older than AO's tested minimum %s",
				init.AgentInfo.Version, minimumCLIACPVersion)
		}
		return nil
	}
	if init.AgentInfo == nil || init.AgentInfo.Name != serverAgentName {
		name := ""
		if init.AgentInfo != nil {
			name = init.AgentInfo.Name
		}
		return fmt.Errorf("expected the %s server or agy acp, got agent %q", serverAgentName, name)
	}
	installed, ok := agentversion.ParsePrefix(init.AgentInfo.Version)
	if !ok {
		return fmt.Errorf("unrecognized %s version %q (Orchestrator requires %s or newer)",
			serverAgentName, init.AgentInfo.Version, minimumServerVersion)
	}
	minimum := agentversion.MustParse(minimumServerVersion)
	if installed.Less(minimum) {
		return fmt.Errorf("%s %s is older than Orchestrator's tested minimum %s",
			serverAgentName, init.AgentInfo.Version, minimumServerVersion)
	}
	return nil
}

// isCLIServer reports whether the handshake came from `agy acp`.
func isCLIServer(init acpsdk.InitializeResponse) bool {
	return init.AgentInfo != nil && init.AgentInfo.Name == cliServerAgentName
}

// cliSteers reports whether the handshake came from an `agy acp` that steers.
func cliSteers(init acpsdk.InitializeResponse) bool {
	if !isCLIServer(init) {
		return false
	}
	installed, ok := agentversion.ParsePrefix(init.AgentInfo.Version)
	return ok && !installed.Less(agentversion.MustParse(minimumCLIACPVersion))
}
