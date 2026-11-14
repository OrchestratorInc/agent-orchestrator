package claudecode

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// OrchestratorAllowedTools pre-approves the two AO commands an orchestrator
// coordinates workers with. Its standing instructions give every new task to a
// new worker through `ao spawn`, and send follow-up through `ao send` (#8398).
// In auto mode Claude Code's classifier refuses a spawn whose task came from
// another session, such as a master orchestrator relaying work, and nothing
// answers that refusal, so the orchestrator stalls until a human approves the
// spawn in chat. Allow rules resolve before the classifier, and narrow rules
// like these stay in effect in auto mode. Each spawned worker still runs under
// its own permission mode.
//
// Claude Code splits a compound command and approves it by rule only when every
// part is approved by a rule. In auto mode a read-only part does not count: it
// waits for classifier review, which then decides the whole command. Agents
// routinely trim AO's output with `| tail -5` or `| head`, so those two read-only
// filters get rules too. A `$(cat <<'EOF' ...)` prompt needs none.
var OrchestratorAllowedTools = []string{
	"Bash(ao spawn *)",
	"Bash(ao send *)",
	"Bash(tail *)",
	"Bash(head *)",
}

// OrchestratorToolRules returns the allow rules an orchestrator starts with.
// They apply only in auto mode, the one mode whose classifier refuses these
// calls with nobody to ask. Other modes keep their own behavior: default mode
// still asks the human, and bypass mode needs no rule.
func OrchestratorToolRules(orchestrator bool, permissions ports.PermissionMode) []string {
	if !orchestrator || ports.NormalizePermissionMode(permissions) != ports.PermissionModeAuto {
		return nil
	}
	return append([]string(nil), OrchestratorAllowedTools...)
}

// withOrchestratorTools adds the orchestrator rules to a terminal session's
// existing allow list.
func withOrchestratorTools(kind domain.SessionKind, permissions ports.PermissionMode, allowed []string) []string {
	rules := OrchestratorToolRules(kind == domain.KindOrchestrator, permissions)
	if len(rules) == 0 {
		return allowed
	}
	return append(append([]string(nil), allowed...), rules...)
}
