package strands

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentRuntimeLaunchEnv = (*Plugin)(nil)

// AugmentRuntimeLaunchEnv fences Strands to the same native home validated by
// command construction and prevents user/project MCP server discovery.
func (p *Plugin) AugmentRuntimeLaunchEnv(env map[string]string, _ string, _ domain.SessionID, _ string) {
	p.nativeHomeMu.RLock()
	home := p.nativeHome
	p.nativeHomeMu.RUnlock()
	if home != "" {
		env["HOME"] = home
		env["USERPROFILE"] = home
	}
	env["STRANDS_CLI_MCP_DISCOVERY"] = "off"
}
