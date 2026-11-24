package copilotacp

import (
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentversion"
)

// Copilot CLI identifies itself as "Copilot" in the ACP handshake. 1.0.93 is
// the oldest build AO has verified end to end over ACP.
const (
	copilotAgentName      = "Copilot"
	minimumCopilotVersion = "1.0.93"
)

// validateInitialize admits only GitHub Copilot CLI at or above the verified
// version. It reads the version from the handshake rather than running
// `copilot --version`, which would start a second CLI process per probe.
func validateInitialize(init acpsdk.InitializeResponse) error {
	if init.AgentInfo == nil {
		return fmt.Errorf("ACP agent did not identify itself; Orchestrator requires GitHub Copilot CLI %s or newer",
			minimumCopilotVersion)
	}
	if init.AgentInfo.Name != copilotAgentName {
		return fmt.Errorf("ACP agent %q is not GitHub Copilot CLI", init.AgentInfo.Name)
	}
	installed, ok := agentversion.Parse(init.AgentInfo.Version)
	if !ok {
		return fmt.Errorf("unrecognized GitHub Copilot CLI version %q (Orchestrator requires %s or newer)",
			init.AgentInfo.Version, minimumCopilotVersion)
	}
	minimum := agentversion.MustParse(minimumCopilotVersion)
	if installed.Less(minimum) {
		return fmt.Errorf("GitHub Copilot CLI %s is older than Orchestrator's tested minimum %s",
			init.AgentInfo.Version, minimumCopilotVersion)
	}
	return nil
}
