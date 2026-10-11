package strands

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentRuntimeLaunchEnv = (*Plugin)(nil)

// AugmentRuntimeLaunchEnv prevents Strands from loading user- or project-level
// MCP servers outside the portable profile validated by command construction.
func (p *Plugin) AugmentRuntimeLaunchEnv(env map[string]string, _ string, _ domain.SessionID, _ string) {
	env["STRANDS_CLI_MCP_DISCOVERY"] = "off"
}
