package codex

import "github.com/aoagents/agent-orchestrator/backend/internal/ports"

// ManagedRouteArgs keeps Chat and terminal launches on the same validated wire contract.
func ManagedRouteArgs(route *ports.AgentProviderRoute) ([]string, error) {
	return accountsManagerRouteFlags(route)
}
