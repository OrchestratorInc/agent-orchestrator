package usage

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// SupportedHarness reports whether the harness has a certified usage pipeline.
func SupportedHarness(h domain.AgentHarness) bool {
	switch h {
	case domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessKimi, domain.HarnessPi, domain.HarnessQwen:
		return true
	default:
		return false
	}
}

// ChatReportedHarness reports whether the harness's usage is recorded from
// the per-turn accounting its chat connection reports, because it has no
// native transcript pipeline. It never overlaps SupportedHarness.
func ChatReportedHarness(h domain.AgentHarness) bool {
	return !SupportedHarness(h) && domain.ChatReportedUsageHarness(h)
}
