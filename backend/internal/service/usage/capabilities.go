package usage

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

func SupportedHarness(h domain.AgentHarness) bool {
	_, ok := sourceKindForHarness(h)
	return ok
}
