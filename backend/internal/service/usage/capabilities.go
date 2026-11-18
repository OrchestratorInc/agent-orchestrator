package usage

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// SupportedHarness reports whether the harness has a certified usage pipeline: one that names the source its usage is read from.
func SupportedHarness(h domain.AgentHarness) bool {
	_, ok := sourceKindForHarness(h)
	return ok
}
