package mcpserver

import (
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// mcpToolGroupEnabled reports whether this server offers a group of tools.
// AO_MCP_TOOLS narrows what the server offers; absent, it offers every group
// it can.
//
// A worker session has no chat thread to publish a page into, so it is given
// the groups that do not need one instead of the whole catalog.
func mcpToolGroupEnabled(group string) bool {
	raw := strings.TrimSpace(os.Getenv(config.EnvMCPToolGroups))
	if raw == "" {
		return true
	}
	return mcpToolGroupNamed(group)
}

// mcpToolGroupNamed reports whether AO_MCP_TOOLS names this group.
//
// A group that spends money is only offered when it is named. The other
// groups are part of what a session is expected to have, so an unset
// AO_MCP_TOOLS leaves them on; the GMI Cloud tools reach GMI's account and
// bill it, so they are handed out only to the sessions AO was told may use
// them.
func mcpToolGroupNamed(group string) bool {
	raw := strings.TrimSpace(os.Getenv(config.EnvMCPToolGroups))
	if raw == "" {
		return false
	}
	for _, name := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(name), group) {
			return true
		}
	}
	return false
}

// htmlTools returns the HTML tool definitions this server offers, or none
// when the group is off.
func (s *server) htmlTools() []map[string]any {
	if !mcpToolGroupEnabled(config.MCPToolGroupHTML) {
		return nil
	}
	return mcpTools
}
