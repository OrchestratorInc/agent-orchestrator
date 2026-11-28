package domain

import "strings"

// DefaultAgentPriority is the order AO prefers agents in when a project names
// none. It must match DEFAULT_AGENT_PRIORITY in the desktop renderer
// (frontend/src/renderer/lib/agent-select-options.ts).
var DefaultAgentPriority = []AgentHarness{
	HarnessClaudeCode, HarnessCodex, HarnessCursor, HarnessOpenCode, HarnessOpenCodeV2, HarnessAider,
}

func defaultAgentPriorityRank(id string) int {
	for i, harness := range DefaultAgentPriority {
		if string(harness) == id {
			return i
		}
	}
	return len(DefaultAgentPriority)
}

// agentLaunchable mirrors isLaunchableAgent: only definite failures rule an
// agent out; unknown readiness is not a rejection.
func agentLaunchable(agent AgentReadinessSnapshot) bool {
	return agent.Installation.State != AgentInstallationNotInstalled &&
		agent.Authentication.State != AgentAuthenticationUnauthorized
}

// agentConfirmed reports an agent AO found installed and signed in.
// "configured" counts: AO cannot verify most credentials.
func agentConfirmed(agent AgentReadinessSnapshot) bool {
	if agent.Installation.State != AgentInstallationInstalled {
		return false
	}
	switch agent.Authentication.State {
	case AgentAuthenticationAuthorized, AgentAuthenticationNotApplicable, AgentAuthenticationConfigured:
		return true
	}
	return false
}

// agentReadinessTier mirrors readinessTier: confirmed agents first, then
// agents found installed whose sign-in is unchecked, then unchecked agents.
func agentReadinessTier(agent AgentReadinessSnapshot) int {
	if agentConfirmed(agent) {
		return 2
	}
	if agent.Installation.State == AgentInstallationInstalled {
		return 1
	}
	return 0
}

// PreferredProjectAgent is the first entry of the renderer's
// preferredAgentList: agents AO found installed and signed in come first, then
// agents it found installed without a sign-in check, then agents it has not
// checked at all, so an agent that may not be installed never beats one that
// is. Within each group the integration priority decides. Missing or
// signed-out agents are left out. It returns "" when no agent can launch.
func PreferredProjectAgent(agents []AgentReadinessSnapshot) AgentHarness {
	var best *AgentReadinessSnapshot
	for i := range agents {
		agent := &agents[i]
		if !agentLaunchable(*agent) {
			continue
		}
		if best == nil || preferAgent(*agent, *best) {
			best = agent
		}
	}
	if best == nil {
		return ""
	}
	return AgentHarness(best.ID)
}

// PreferredInstalledProjectAgent is PreferredProjectAgent when AO has found at
// least one launchable agent installed, and "" otherwise. A pick saved to a
// project outlives the checks, so it needs that evidence: with every agent
// unchecked it would always save Claude Code.
func PreferredInstalledProjectAgent(agents []AgentReadinessSnapshot) AgentHarness {
	for _, agent := range agents {
		if agentLaunchable(agent) && agent.Installation.State == AgentInstallationInstalled {
			// Installed agents rank above unchecked ones, so the top pick is one.
			return PreferredProjectAgent(agents)
		}
	}
	return ""
}

func preferAgent(a, b AgentReadinessSnapshot) bool {
	if at, bt := agentReadinessTier(a), agentReadinessTier(b); at != bt {
		return at > bt
	}
	return priorityLess(a, b)
}

// PreferredProjectAgentNeedsChecks mirrors preferredAgentNeedsChecks: the
// highest-priority usable agent is not confirmed yet, so running the pending
// checks could change the pick. A snapshot taken before the first checks reads
// "unknown" for every agent and would otherwise always pick Claude Code.
func PreferredProjectAgentNeedsChecks(agents []AgentReadinessSnapshot) bool {
	var first *AgentReadinessSnapshot
	for i := range agents {
		agent := &agents[i]
		if !agentLaunchable(*agent) {
			continue
		}
		if first == nil || priorityLess(*agent, *first) {
			first = agent
		}
	}
	return first != nil && !agentConfirmed(*first)
}

func priorityLess(a, b AgentReadinessSnapshot) bool {
	if ar, br := defaultAgentPriorityRank(a.ID), defaultAgentPriorityRank(b.ID); ar != br {
		return ar < br
	}
	if c := strings.Compare(a.Label, b.Label); c != 0 {
		return c < 0
	}
	return a.ID < b.ID
}

// MissingRoleAgent reports whether the worker or orchestrator role names no agent.
func (c ProjectConfig) MissingRoleAgent() bool {
	return c.Worker.Harness == "" || c.Orchestrator.Harness == ""
}

// FillRoleAgents assigns agent to each empty role and clears its
// provider-specific tuning. It reports whether anything changed.
func (c *ProjectConfig) FillRoleAgents(agent AgentHarness) bool {
	if agent == "" {
		return false
	}
	changed := false
	for _, role := range []*RoleOverride{&c.Worker, &c.Orchestrator} {
		if role.Harness != "" {
			continue
		}
		role.Harness = agent
		role.AgentConfig.Model = ""
		role.AgentConfig.Mode = ""
		role.AgentConfig.Effort = ""
		changed = true
	}
	return changed
}
