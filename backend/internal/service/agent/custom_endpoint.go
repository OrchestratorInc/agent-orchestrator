package agent

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
)

// authenticationOverride answers readiness for a harness the user pointed at
// their own API endpoint before any other check: AO injects that base URL and
// key into every session of the harness, so the harness's own login no longer
// decides how it authenticates. AO cannot prove an arbitrary gateway accepts
// the key, so the state is configured, not authorized. Everything else keeps
// the account-aware check.
func (s *Service) authenticationOverride(ctx context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
	if s.dataDir != "" {
		// The same check every launch makes: a saved endpoint that cannot be
		// read or used stops launches, so readiness must not fall back to the
		// native login and report the harness ready.
		env, err := harnessendpoint.ForDataDir(s.dataDir).Env(agentID)
		if err != nil {
			return failedAuthentication(s.now(), domain.AgentReadinessReasonAuthCheckFailed, harnessendpoint.UnusableMessage), true
		}
		if env != nil {
			return successfulAuthentication(s.now(), domain.AgentAuthenticationConfigured, domain.AgentReadinessReasonAuthConfigured,
				"Using a custom API endpoint. Orchestrator has not verified the key."), true
		}
	}
	return s.authenticationWithAccount(ctx, agentID, purpose)
}
